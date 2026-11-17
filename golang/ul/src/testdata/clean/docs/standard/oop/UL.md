# OOP

durian.cloud > engineering > standards > OOP

When writing code, best practice when using object-oriented programming

| Word | Means | Also called | See |
|---|---|---|---|
| OCP | Add behaviour by adding a plugin, not by editing shared code. | Open-Close Principle, Plugin Design | [Plugin](#plugin), [SRP](#srp) |
| Plugin | One part added to the core through one seam. |  | [OCP](#ocp) |
| SRP | A module has one reason to change. | Single Responsibility |  |

Retired words:

| Word | Replaced by |
|---|---|
| Facade | [Plugin](#plugin) |

## OCP

Code is **open** for extension and **closed** for edits.

Add a plugin that implements the seam; never rewrite another plugin's line.

Avoid: extension point

## Plugin

A plugin owns its own types and joins the core with one registration line.

## SRP

Pairs with the Open-Close Principle when a plugin grows.

## Facade

Old name for the one entry object of a plugin.

Retired. Use [Plugin](#plugin) instead.
