package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

// configPath is set at build time via -ldflags "-X main.configPath=..."
var configPath = "/etc/prompt-sudo-discord/config.json"

const defaultTimeout = 300

// Button custom IDs
const (
	buttonApproveID = "psd_approve"
	buttonDenyID    = "psd_deny"
)

type Config struct {
	DiscordToken   string   `json:"discord_token"`
	ApproverIDs    []string `json:"approver_ids"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

type ApprovalResult int

const (
	ApprovalPending ApprovalResult = iota
	ApprovalApproved
	ApprovalDenied
	ApprovalTimeout
	ApprovalError
)

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if config.DiscordToken == "" {
		return nil, fmt.Errorf("discord_token is required")
	}
	if len(config.ApproverIDs) == 0 {
		return nil, fmt.Errorf("approver_ids is required")
	}
	if config.TimeoutSeconds <= 0 {
		config.TimeoutSeconds = defaultTimeout
	}

	return &config, nil
}

func isApprover(userID string, approverIDs []string) bool {
	for _, id := range approverIDs {
		if id == userID {
			return true
		}
	}
	return false
}

func formatCommand(args []string) string {
	// Simple formatting - in production might want to escape properly
	return strings.Join(args, " ")
}

func main() {
	// Parse flags
	channelID := flag.String("channel", "", "Discord channel ID to post approval request")
	replyTo := flag.String("reply-to", "", "Message ID to reply to (optional)")
	timeout := flag.Int("timeout", 0, "Timeout in seconds (default: from config or 300)")
	showStdin := flag.Bool("show-stdin", false, "Read stdin and include it in the approval request")
	// Config path is hardcoded - cannot be overridden by arguments for security

	flag.Parse()

	// Get command to execute (everything after --)
	commandArgs := flag.Args()
	if len(commandArgs) == 0 {
		fmt.Fprintln(os.Stderr, "Error: No command specified")
		fmt.Fprintln(os.Stderr, "Usage: prompt-sudo-discord --channel CHANNEL_ID [--reply-to MSG_ID] -- COMMAND [ARGS...]")
		os.Exit(1)
	}

	if *channelID == "" {
		fmt.Fprintln(os.Stderr, "Error: --channel is required")
		os.Exit(1)
	}

	// Read stdin if --show-stdin is enabled
	var stdinData []byte
	if *showStdin {
		var err error
		stdinData, err = io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
	}

	// Load config (path is set at build time)
	config, err := loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Use timeout from flag, config, or default
	timeoutSec := config.TimeoutSeconds
	if *timeout > 0 {
		timeoutSec = *timeout
	}

	// Format command for display
	commandStr := formatCommand(commandArgs)

	// Create Discord session
	dg, err := discordgo.New(config.DiscordToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Discord session: %v\n", err)
		os.Exit(1)
	}

	// No specific intents needed; interactions arrive via the gateway regardless

	// Channel for approval result
	resultCh := make(chan ApprovalResult, 1)
	var requestMsgID string

	// Interaction handler (button clicks)
	dg.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type != discordgo.InteractionMessageComponent {
			return
		}

		// Only process interactions on our request message
		if i.Message == nil || i.Message.ID != requestMsgID {
			return
		}

		// Check if user is an approver
		userID := ""
		if i.Member != nil {
			userID = i.Member.User.ID
		} else if i.User != nil {
			userID = i.User.ID
		}
		if !isApprover(userID, config.ApproverIDs) {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "⚠️ You are not an authorized approver.",
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			})
			return
		}

		customID := i.MessageComponentData().CustomID

		switch customID {
		case buttonApproveID:
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseDeferredMessageUpdate,
			})
			select {
			case resultCh <- ApprovalApproved:
			default:
			}
		case buttonDenyID:
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseDeferredMessageUpdate,
			})
			select {
			case resultCh <- ApprovalDenied:
			default:
			}
		}
	})

	// Open websocket connection
	err = dg.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening Discord connection: %v\n", err)
		os.Exit(1)
	}
	defer dg.Close()

	// Build the request message
	hostname, _ := os.Hostname()
	cwd, _ := os.Getwd()

	requestContent := fmt.Sprintf("**🔐 Sudo Request**\n"+
		"```\n%s\n```\n"+
		"**Host:** `%s`\n"+
		"**CWD:** `%s`\n"+
		"**Timeout:** %ds",
		commandStr, hostname, cwd, timeoutSec)

	if *showStdin {
		stdinDisplay := string(stdinData)
		// Discord message limit is 2000 chars; reserve space for the rest of the message
		maxStdinDisplay := 2000 - len(requestContent) - len("\n**Stdin:**\n```\n\n```") - 50
		if maxStdinDisplay < 0 {
			maxStdinDisplay = 0
		}
		if len(stdinDisplay) > maxStdinDisplay {
			stdinDisplay = stdinDisplay[:maxStdinDisplay] + fmt.Sprintf("\n... (%d bytes truncated)", len(stdinDisplay)-maxStdinDisplay)
		}
		requestContent += fmt.Sprintf("\n**Stdin:**\n```\n%s\n```", stdinDisplay)
	}

	msgSend := &discordgo.MessageSend{
		Content: requestContent,
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "Approve",
						Style:    discordgo.SuccessButton,
						CustomID: buttonApproveID,
						Emoji: &discordgo.ComponentEmoji{
							Name: "✅",
						},
					},
					discordgo.Button{
						Label:    "Deny",
						Style:    discordgo.DangerButton,
						CustomID: buttonDenyID,
						Emoji: &discordgo.ComponentEmoji{
							Name: "❌",
						},
					},
				},
			},
		},
	}
	if *replyTo != "" {
		msgSend.Reference = &discordgo.MessageReference{
			MessageID: *replyTo,
			ChannelID: *channelID,
		}
	}

	// Send the request message
	var msg *discordgo.Message
	msg, err = dg.ChannelMessageSendComplex(*channelID, msgSend)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending Discord message: %v\n", err)
		os.Exit(1)
	}
	requestMsgID = msg.ID

	fmt.Fprintf(os.Stderr, "Approval request sent (message ID: %s)\n", requestMsgID)
	fmt.Fprintf(os.Stderr, "Waiting for approval (timeout: %ds)...\n", timeoutSec)

	// Setup context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	// Handle interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Wait for result
	var result ApprovalResult
	select {
	case result = <-resultCh:
		// Got a response
	case <-ctx.Done():
		result = ApprovalTimeout
	case <-sigCh:
		fmt.Fprintln(os.Stderr, "\nInterrupted")
		// Update Discord message - remove buttons and show cancelled status
		cancelContent := requestContent + "\n\n⚠️ **Cancelled** (interrupted)."
		dg.ChannelMessageEditComplex(&discordgo.MessageEdit{
			ID:         requestMsgID,
			Channel:    *channelID,
			Content:    &cancelContent,
			Components: &[]discordgo.MessageComponent{},
		})
		os.Exit(130)
	}

	// disableButtons edits the original message to remove buttons and append a status line
	disableButtons := func(status string) {
		editContent := requestContent + "\n\n" + status
		dg.ChannelMessageEditComplex(&discordgo.MessageEdit{
			ID:         requestMsgID,
			Channel:    *channelID,
			Content:    &editContent,
			Components: &[]discordgo.MessageComponent{},
		})
	}

	// Handle result
	switch result {
	case ApprovalApproved:
		fmt.Fprintln(os.Stderr, "✅ Approved! Executing command...")

		disableButtons("✅ **Approved.** Executing...")

		// Close Discord connection before exec
		dg.Close()

		if *showStdin {
			// Use os/exec to pipe buffered stdin to the command
			cmd := exec.Command(commandArgs[0], commandArgs[1:]...)
			cmd.Stdin = bytes.NewReader(stdinData)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			err := cmd.Run()
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					os.Exit(exitErr.ExitCode())
				}
				fmt.Fprintf(os.Stderr, "Error executing command: %v\n", err)
				os.Exit(1)
			}
		} else {
			// Replace current process with the command
			execPath, err := exec.LookPath(commandArgs[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error finding executable: %v\n", err)
				os.Exit(1)
			}
			err = syscall.Exec(execPath, commandArgs, os.Environ())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error executing command: %v\n", err)
				os.Exit(1)
			}
		}

	case ApprovalDenied:
		fmt.Fprintln(os.Stderr, "❌ Denied.")
		disableButtons("❌ **Denied.**")
		os.Exit(1)

	case ApprovalTimeout:
		fmt.Fprintln(os.Stderr, "⏰ Timeout.")
		disableButtons(fmt.Sprintf("⏰ **Timed out** after %ds.", timeoutSec))
		os.Exit(1)

	default:
		fmt.Fprintln(os.Stderr, "Unknown error")
		os.Exit(1)
	}
}
