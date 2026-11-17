# Ubiquitous language

## OOP

durian.cloud > engineering > standards > OOP

When writing code, best practice when using object-oriented programming

| Word | Means | Also called | See |
|---|---|---|---|
| OCP | Add behaviour by adding a plugin, not by editing shared code. | Open-Close Principle, Plugin Design | Plugin, SRP |
| Plugin | One part added to the core through one seam. |  | OCP |
| SRP | A module has one reason to change. | Single Responsibility |  |

Retired words:

| Word | Replaced by |
|---|---|
| Facade | Plugin |

### OCP

Code is **open** for extension and **closed** for edits.

Add a plugin that implements the seam; never rewrite another plugin's line.

Avoid: extension point

### Plugin

A plugin owns its own types and joins the core with one registration line.

### SRP

Pairs with the Open-Close Principle when a plugin grows.

### Facade

Old name for the one entry object of a plugin.

Retired. Use Plugin instead.

## billing

durian.cloud > shop > billing

Money owed by a customer, from the first charge to the last receipt

| Word | Means | Also called | See |
|---|---|---|---|
| Charge | One amount added to an invoice. |  | Invoice |
| Invoice | The bill sent to a customer for a period. |  | Charge, durian.cloud/engineering/standards/OOP#OCP |
| Plugin | A payment method that the billing context can take money with. | Payment Plugin |  |

### Charge

### Invoice

An invoice lists every Charge of the period.

It is final once sent.

Avoid: bill, statement

### Plugin

## pay

durian.cloud > shop > billing > pay

The card payment plugin of billing

| Word | Means | Also called | See |
|---|---|---|---|
| Receipt | Proof that a Charge was paid. |  | durian.cloud/shop/billing#Charge |
| Settle | Move the money of one Invoice from the card to the shop. |  | durian.cloud/shop/billing#Invoice |

### Receipt

Issued once; a Plugin never edits it.

### Settle
