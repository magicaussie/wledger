#!/usr/bin/env bash
set -euo pipefail

out_dir="web/icons"
# Create the output directory if it doesn't exist
mkdir -p "$out_dir"

icons=(
  arrow-right-left
  box
  clipboard
  eye
  file
  github
  grip-horizontal
  import
  languages
  layout-grid
  lightbulb
  lock
  log-in
  log-out
  map-pin
  menu
  microchip
  minus
  moon
  pencil
  plus
  power
  refresh-ccw
  settings
  sun
  swatch-book
  trash
  triangle-alert
  users
  x
  zap
)

echo "Building golucide tool..."

# Build the tool to a temporary location
tmp_bin="./tmp/tmp_golucide"
go build -o "$tmp_bin" github.com/dimmerz92/go-lucide-icons/cmd/golucide

echo "Generating icons..."

# Loop through icons
for icon in "${icons[@]}"; do
  # Run the command using the local binary
  # The '&' at the end runs the command in the background
  "$tmp_bin" templ "$icon" -out "$out_dir" &
done

# Wait for all background jobs to finish
wait

# Cleanup the temporary binary
rm "$tmp_bin"

echo "Done! Generated ${#icons[@]} icons."