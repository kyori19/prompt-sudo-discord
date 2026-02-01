# prompt-sudo-discord

Discord-based sudo approval system. Requests privileged command execution and waits for approval from an authorized Discord user.

## Security Model

- Binary is owned by root, not writable by unprivileged users
- Config file (`/etc/prompt-sudo-discord/config.json`) is root-readable only
- Statically linked Go binary - no runtime dependencies to hijack
- Discord API is called directly (not through any intermediary process)
- Only specified Discord user IDs can approve requests

## Build

```bash
# Build using Docker (recommended - clean environment)
./build.sh

# Or with custom config path
./build.sh /custom/path/config.json

# Or build locally
CGO_ENABLED=0 go build -ldflags="-s -w -X main.configPath=/etc/prompt-sudo-discord/config.json" -o prompt-sudo-discord
```

## Install

```bash
# Install binary
sudo cp dist/prompt-sudo-discord /usr/local/bin/
sudo chown root:root /usr/local/bin/prompt-sudo-discord
sudo chmod 755 /usr/local/bin/prompt-sudo-discord

# Install config
sudo mkdir -p /etc/prompt-sudo-discord
sudo cp config.example.json /etc/prompt-sudo-discord/config.json
sudo chown root:root /etc/prompt-sudo-discord/config.json
sudo chmod 600 /etc/prompt-sudo-discord/config.json
# Edit config with your Discord token and approver IDs

# Setup sudoers (replace <USERNAME> with the user who will run the command)
echo '<USERNAME> ALL=(root) NOPASSWD: /usr/local/bin/prompt-sudo-discord' | sudo tee /etc/sudoers.d/prompt-sudo-discord
sudo chmod 440 /etc/sudoers.d/prompt-sudo-discord
```

## Usage

```bash
sudo /usr/local/bin/prompt-sudo-discord \
  --channel "CHANNEL_ID" \
  --reply-to "MESSAGE_ID" \
  -- apt update
```

### Parameters

- `--channel` (required): Discord channel ID to post the approval request
- `--reply-to` (optional): Message ID to reply to
- `--timeout` (optional): Timeout in seconds (default: 300)
- `--` : Separator before the command to execute

## Approval

React to the approval request message with:
- ✅ 👍 ☑️ 🆗 - Approve (execute the command)
- ❌ 👎 🚫 ⛔ - Deny (reject the request)

Only users listed in `approver_ids` config can approve/deny.

## Config

`/etc/prompt-sudo-discord/config.json`:

```json
{
  "discord_token": "Bot YOUR_BOT_TOKEN_HERE",
  "approver_ids": ["YOUR_DISCORD_USER_ID"],
  "timeout_seconds": 300
}
```

Note: `discord_token` must be prefixed with `Bot ` (including the space).

## License

MIT
