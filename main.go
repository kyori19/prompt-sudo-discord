package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
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

// Reaction emojis for approval/denial
var (
	approveEmojis = []string{"✅", "👍", "☑️", "🆗"}
	denyEmojis    = []string{"❌", "👎", "🚫", "⛔"}
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

func containsEmoji(emoji string, list []string) bool {
	for _, e := range list {
		if e == emoji {
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
	
	// We need to receive reaction events
	dg.Identify.Intents = discordgo.IntentsGuildMessageReactions | discordgo.IntentsDirectMessageReactions
	
	// Channel for approval result
	resultCh := make(chan ApprovalResult, 1)
	var requestMsgID string
	
	// Reaction handler
	dg.AddHandler(func(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
		// Only process reactions on our request message
		if r.MessageID != requestMsgID {
			return
		}
		
		// Check if user is an approver
		if !isApprover(r.UserID, config.ApproverIDs) {
			return
		}
		
		// Check the emoji
		emoji := r.Emoji.Name
		if containsEmoji(emoji, approveEmojis) {
			select {
			case resultCh <- ApprovalApproved:
			default:
			}
		} else if containsEmoji(emoji, denyEmojis) {
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
		"**Timeout:** %ds\n\n"+
		"React with ✅ to approve or ❌ to deny.",
		commandStr, hostname, cwd, timeoutSec)
	
	// Send the request message
	var msg *discordgo.Message
	if *replyTo != "" {
		msg, err = dg.ChannelMessageSendReply(*channelID, requestContent, &discordgo.MessageReference{
			MessageID: *replyTo,
			ChannelID: *channelID,
		})
	} else {
		msg, err = dg.ChannelMessageSend(*channelID, requestContent)
	}
	
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
		// Update Discord message
		dg.ChannelMessageSend(*channelID, fmt.Sprintf("⚠️ Request `%s` was **cancelled** (interrupted).", requestMsgID))
		os.Exit(130)
	}
	
	// Handle result
	switch result {
	case ApprovalApproved:
		fmt.Fprintln(os.Stderr, "✅ Approved! Executing command...")
		
		// Update Discord
		dg.ChannelMessageSend(*channelID, fmt.Sprintf("✅ Request `%s` **approved**. Executing...", requestMsgID))
		
		// Close Discord connection before exec
		dg.Close()
		
		// Find the executable path
		execPath, err := exec.LookPath(commandArgs[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding executable: %v\n", err)
			os.Exit(1)
		}
		
		// Replace current process with the command
		err = syscall.Exec(execPath, commandArgs, os.Environ())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error executing command: %v\n", err)
			os.Exit(1)
		}
		
	case ApprovalDenied:
		fmt.Fprintln(os.Stderr, "❌ Denied.")
		dg.ChannelMessageSend(*channelID, fmt.Sprintf("❌ Request `%s` **denied**.", requestMsgID))
		os.Exit(1)
		
	case ApprovalTimeout:
		fmt.Fprintln(os.Stderr, "⏰ Timeout.")
		dg.ChannelMessageSend(*channelID, fmt.Sprintf("⏰ Request `%s` **timed out** after %ds.", requestMsgID, timeoutSec))
		os.Exit(1)
		
	default:
		fmt.Fprintln(os.Stderr, "Unknown error")
		os.Exit(1)
	}
}
