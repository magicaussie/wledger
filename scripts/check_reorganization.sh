#!/bin/bash
if [ -f "docs/Software/quickstart-guide.md" ] && [ -f "docs/Software/feature-guide.md" ] && [ -f "docs/Software/developer-guide.md" ] && [ -f "docs/Hardware/build-guide.md" ]; then
  echo "File reorganization successful."
  exit 0
else
  echo "Reorganization FAILED."
  exit 1
fi
