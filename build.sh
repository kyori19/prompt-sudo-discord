#!/bin/bash
set -e

cd "$(dirname "$0")"

IMAGE_NAME="prompt-sudo-discord-builder"
CONTAINER_NAME="prompt-sudo-discord-extract"
CONFIG_PATH="${1:-/etc/prompt-sudo-discord/config.json}"

echo "Building prompt-sudo-discord with Docker..."
echo "Config path: $CONFIG_PATH"

# Create dist directory
mkdir -p dist

# Build the Docker image (use host network to avoid iptables issues)
docker build --network host --build-arg "CONFIG_PATH=$CONFIG_PATH" -t "$IMAGE_NAME" .

# Extract binary from container
docker create --name "$CONTAINER_NAME" "$IMAGE_NAME" >/dev/null
docker cp "$CONTAINER_NAME:/prompt-sudo-discord" dist/prompt-sudo-discord
docker rm "$CONTAINER_NAME" >/dev/null

chmod +x dist/prompt-sudo-discord

# Clean up image
docker rmi "$IMAGE_NAME" >/dev/null 2>&1 || true

echo ""
echo "Build complete!"
echo "Binary: dist/prompt-sudo-discord"
echo ""
echo "To verify it's static:"
echo "  file dist/prompt-sudo-discord"
echo ""
echo "To install:"
echo "  sudo cp dist/prompt-sudo-discord /usr/local/bin/"
echo "  sudo chown root:root /usr/local/bin/prompt-sudo-discord"
echo "  sudo chmod 755 /usr/local/bin/prompt-sudo-discord"
