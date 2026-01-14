#!/bin/bash
if [ -f "website/docusaurus.config.ts" ] || [ -f "website/docusaurus.config.js" ]; then
  echo "Docusaurus initialized successfully."
  exit 0
else
  echo "Docusaurus not initialized."
  exit 1
fi
