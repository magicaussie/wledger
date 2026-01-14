#!/bin/bash
cd website
npm run build
if [ $? -eq 0 ]; then
  echo "Build successful."
  exit 0
else
  echo "Build FAILED."
  exit 1
fi
