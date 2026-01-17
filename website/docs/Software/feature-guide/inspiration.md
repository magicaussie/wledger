---
title: Inspiration
sidebar_position: 3
---

# Inspiration (LLM Prompts)

WLEDger can help you decide what to build next by generating structured prompts for Large Language Models (LLMs) like Gemini, ChatGPT, or Claude based on your *actual* current inventory. This turns your component list into a powerful project brainstorming tool.

I built this feature for fun, and I've found it to be a great way to overcome the "I want to make something... but what?" feeling.

:::tip A Note on AI
Keeping your inventory data private matters. As such, there are no AI provider services integrated into WLEDger itself.

This is a text-only feature that copies information to your clipboard.
:::

:::tip Pro Tip
Want to export a list of your component names and quantities? Create an empty template and click `Copy Prompt`.
:::

## Default Prompts

There are 3 default prompts included with WLEDger out of the box. You can delete or modify them if you wish.

  1. **Learning Path** - Provides a guided learning path to understand how to use and integrate the selected parts together.
  2. **Missing Link Recommendations** - Recommends a few parts you could add to your inventory to expand the types of projects you could build.
  3. **Project Ideas** - Get inspired! Recommends projects to build based on your current inventory

## Important Considerations

*   **Privacy:** When you paste a copied prompt into an LLM, you are sharing a summary of your inventory with that service provider (e.g., Google, OpenAI, etc). Be aware of this if your inventory is sensitive, or you don't want to share this data with a 3rd party. If this matters to you, consider using a local LLM instead.
*   **AI Hallucinations:** Large Language Models are probabilistic and [may suggest a project that requires a component you don't actually have, or suggest an incorrect pinout](https://media3.giphy.com/media/v1.Y2lkPTc5MGI3NjExeDB6b2ZubjQ4azhyczcyYWptYmIyM25mc3NnNHYzbjk3NmwwcW9qcCZlcD12MV9pbnRlcm5hbF9naWZfYnlfaWQmY3Q9Zw/1nJZopel7e3Jaf7Rhb/giphy.gif). **Always verify AI-generatedcontent and suggestions against official datasheets and trusted sources.** This feature was developed to spark fun, not fires.