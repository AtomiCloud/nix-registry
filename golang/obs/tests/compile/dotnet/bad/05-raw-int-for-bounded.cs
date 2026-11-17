// expect: CS1503
using Checkout.Signals;
var l = new CheckoutChargesLabels(Provider.Stripe, Outcome.Ok, 3, default);
