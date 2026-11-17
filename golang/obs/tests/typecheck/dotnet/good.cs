// Good consumer: the way the call surface (lib/dotnet-interfaces) uses the generated types.
using System.Reflection;
using Checkout.Signals;
using AtomiCloud.Diene.Interfaces;

var shard = Int<ShardSpec>.Of(3);
if (shard is not Result<Int<ShardSpec>>.Ok s)
{
    throw new Exception("3 refused");
}

if (Int<ShardSpec>.Of(16) is not Result<Int<ShardSpec>>.Err)
{
    throw new Exception("16 accepted");
}

var hour = (Result<Int<HourSpec>>.Ok)Int<HourSpec>.AtTime(
    new DateTimeOffset(2026, 11, 17, 4, 0, 0, TimeSpan.Zero));
var day = DurianSignalDate<BillingDaySpec>.Of(new DateOnly(2026, 11, 17));
var labels = new CheckoutChargesLabels(Provider.Stripe, Outcome.Ok, s.Value, hour.Value);
var invoices = new CheckoutInvoicesLabels(day, Provider.Adyen);
DurianSignalCounter<CheckoutChargesLabels> counter = Names.CheckoutCharges;
Event<ErrorLevel> failed = Events.ChargeFailed;
foreach (var p in typeof(CheckoutChargesLabels).GetProperties())
{
    var wire = p.GetCustomAttribute<DurianSignalLabelAttribute>()!.Name;
    var v = (IDurianSignalWire)p.GetValue(labels)!;
    Console.WriteLine($"label {wire}={v.Wire} valid={v.Valid}");
}

var keys = string.Join(",", SignalTables.Metrics["checkout.charges"].Keys);
Console.WriteLine($"{counter.Name} {failed.Name} {failed.Level} {invoices.BillingDay.Wire} {keys}");
Console.WriteLine($"{Spans.CheckoutProviderCall.Kind} default-valid={default(Int<ShardSpec>).Valid}");
Console.WriteLine(new CheckoutChargeAttributes("u1", Provider.Paypal, s.Value).UserId);

_ = Port.Add(Names.CheckoutCharges, labels);
_ = Log.Error(Events.ChargeFailed);

if (Port.Add(Names.CheckoutCharges, labels with { Shard = default }) is not Result<bool>.Err) throw new Exception("default accepted");
if (Port.Add(Names.CheckoutCharges, labels with { Provider = null! }) is not Result<bool>.Err) throw new Exception("null enum accepted");
