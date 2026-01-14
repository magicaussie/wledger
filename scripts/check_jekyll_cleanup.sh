#!/bin/bash
if [ ! -f "docs/_config.yml" ] && [ -f "website/static/img/favicon.ico" ] && [ -f "_config.yml.bak" ]; then
  echo "Jekyll cleanup and favicon migration successful."
  exit 0
else
  echo "Cleanup/Migration FAILED."
  exit 1
fi
