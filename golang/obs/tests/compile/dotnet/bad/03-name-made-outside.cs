// expect: CS0122
using Checkout.Signals;
var n = new DurianSignalCounter<CheckoutChargesLabels>("checkout.nothing");
