---
title: Lines
concept: lines
description: An avoided word on two lines of one paragraph, and labels that differ in case.
namespaces:
  - example.org/cooking/kitchen
---

Use the Frying pan, never a griddle
and never a second griddle
on the next line.

```chart
kind: line
title: Counts
y: { label: Griddle }
x: { label: griddle, values: [1, 2, 3] }
series:
  - name: Counts
    data: [1, 2, 3]
```
