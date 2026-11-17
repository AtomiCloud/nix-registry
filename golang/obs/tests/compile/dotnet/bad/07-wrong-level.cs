// expect: CS0029
using Checkout.Signals;
DurianSignalEvent<DurianSignalLevelWarn> e = Events.ChargeFailed;
