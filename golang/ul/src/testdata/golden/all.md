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

## billing

durian.cloud > shop > billing

Money owed by a customer, from the first charge to the last receipt

| Word | Means | Also called | See |
|---|---|---|---|
| Charge | One amount added to an invoice. |  | Invoice |
| Invoice | The bill sent to a customer for a period. |  | Charge, durian.cloud/engineering/standards/OOP#OCP |
| Plugin | A payment method that the billing context can take money with. | Payment Plugin |  |

## pay

durian.cloud > shop > billing > pay

The card payment plugin of billing

| Word | Means | Also called | See |
|---|---|---|---|
| Receipt | Proof that a Charge was paid. |  | durian.cloud/shop/billing#Charge |
| Settle | Move the money of one Invoice from the card to the shop. |  | durian.cloud/shop/billing#Invoice |
