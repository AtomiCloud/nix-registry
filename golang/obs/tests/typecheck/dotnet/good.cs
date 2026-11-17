// Good consumer: the way the call surface (lib/dotnet-interfaces) uses the generated types.
using System.Reflection;
using Checkout.Signals;

var shard = DurianSignalInt<ShardSpec>.Of(3);
if (shard is not DurianSignalResult<DurianSignalInt<ShardSpec>>.Ok s)
{
    throw new Exception("3 refused");
}

if (DurianSignalInt<ShardSpec>.Of(16) is not DurianSignalResult<DurianSignalInt<ShardSpec>>.Err)
{
    throw new Exception("16 accepted");
}

var hour = (DurianSignalResult<DurianSignalInt<HourSpec>>.Ok)DurianSignalInt<HourSpec>.AtTime(
    new DateTimeOffset(2026, 11, 17, 4, 0, 0, TimeSpan.Zero));
var day = DurianSignalDate<BillingDaySpec>.Of(new DateOnly(2026, 11, 17));
var labels = new CheckoutChargesLabels(Provider.Stripe, Outcome.Ok, s.Value, hour.Value);
var invoices = new CheckoutInvoicesLabels(day, Provider.Adyen);
DurianSignalCounter<CheckoutChargesLabels> counter = Names.CheckoutCharges;
DurianSignalEvent<DurianSignalLevelError> failed = Events.ChargeFailed;
foreach (var p in typeof(CheckoutChargesLabels).GetProperties())
{
    var wire = p.GetCustomAttribute<DurianSignalLabelAttribute>()!.Name;
    var v = (IDurianSignalWire)p.GetValue(labels)!;
    Console.WriteLine($"label {wire}={v.Wire} valid={v.Valid}");
}

var keys = string.Join(",", SignalTables.Metrics["checkout.charges"].Keys);
Console.WriteLine($"{counter.Name} {failed.Name} {failed.Level} {invoices.BillingDay.Wire} {keys}");
Console.WriteLine($"{Spans.CheckoutProviderCall.Kind} default-valid={default(DurianSignalInt<ShardSpec>).Valid}");
Console.WriteLine(new CheckoutChargeAttributes("u1", Provider.Paypal, s.Value).UserId);
