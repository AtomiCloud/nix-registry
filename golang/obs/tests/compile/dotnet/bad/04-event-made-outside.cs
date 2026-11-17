// expect: CS0122
using Checkout.Signals;
var e = new DurianSignalEvent<DurianSignalLevelError>("checkout.nothing");
