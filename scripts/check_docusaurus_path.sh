#!/bin/bash
if grep -q "path: '../docs'" website/docusaurus.config.ts; then
  echo "Docusaurus configured to use root docs/ directory."
  exit 0
else
  echo "Docusaurus NOT configured correctly."
  exit 1
fi
