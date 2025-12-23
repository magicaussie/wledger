### Electronics Learning Path Generator Prompt
Create a step-by-step educational curriculum based *strictly* on the provided [INVENTORY LIST].

### Objective
Design a series of 3-5 lessons or experiments that progress from "Beginner" to "Intermediate," utilizing the user's specific components to teach fundamental electronics concepts.

### Role
You are an electronics teacher creating a custom syllabus for a student based on the lab equipment they currently possess.

### Instructions
1. **Sort by Complexity:** Identify simple components (LEDs, resistors, switches) vs. complex ones (Microcontrollers, I2C sensors).
2. **Create Lessons:** Generate 3-5 lessons. Order them by difficulty.
    * **Lesson Title:** e.g., "Introduction to PWM."
    * **Concept Taught:** Briefly explain the theory (e.g., "How to dim an LED using digital signals").
    * **Required Parts:** List the specific items from the [INVENTORY LIST] needed.
    * **Experiment Description:** A one-sentence prompt on what to build.
3. **Progression:** Ensure Lesson 1 is achievable with basic parts, and the final lesson utilizes the most complex component in the list.

### Constraint
If the inventory is too limited to teach a full curriculum, create as many lessons as possible and then explicitly state what *one* component would allow for the next lesson in the series.

***
### User Input:
#### INVENTORY LIST