---
title: Data
concept: data
description: Part data that is not prose, and an entity in prose.
namespaces:
  - example.org/cooking/kitchen
type: algorithm
code:
  - shop/src/a.ts#a
  - shop/src/b.ts#b
  - shop/src/c.ts#c
banner:
  kind: info
  text: A banner with a griddle.
---

An entity: a gr&#105;ddle.

```chart
kind: line
title: Chart title
let: { griddle: "range(1, 4)", label: griddle }
x: { label: n, values: [1, 2, 3] }
y: { label: count }
series:
  - name: Counts
    data: [1, 2, 3]
```
