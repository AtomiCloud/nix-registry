// Good consumer: the way the call surface (lib/dotnet-interfaces) uses the generated types. It RUNS: the port
// is built from the generated tables, every valid call is Ok and nothing is dropped.
using System.Reflection;
using Checkout.Signals;
using AtomiCloud.Diene.Interfaces;
using AtomiCloud.Diene.Result;

Port.Install(SignalTables.Metrics, SignalTables.EventLevels);
var shard = Int<ShardSpec>.Of(3);
if (!shard.IsSuccess)
{
    throw new Exception("3 refused");
}

if (Int<ShardSpec>.Of(16).IsSuccess)
{
    throw new Exception("16 accepted");
}

var hour = Int<HourSpec>.AtTime(new DateTimeOffset(2026, 11, 17, 4, 0, 0, TimeSpan.Zero)).Value;
var day = DurianSignalDate<BillingDaySpec>.Of(new DateOnly(2026, 11, 17));
var labels = new CheckoutChargesLabels(Provider.Stripe, Outcome.Ok, shard.Value, hour);
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
Console.WriteLine(new CheckoutChargeAttributes("u1", Provider.Paypal, shard.Value).UserId);

foreach (var r in new[]
{
    Port.Add(Names.CheckoutCharges, labels),
    Port.Add(Names.CheckoutInvoices, invoices),
    Port.Record(Names.CheckoutChargeDuration, new CheckoutChargeDurationLabels(Provider.Adyen, Outcome.Declined)),
    Log.Error(Events.ChargeFailed),
    Log.Warn(Events.ChargeDeclined),
})
{
    if (!r.IsSuccess) throw new Exception($"a valid call was refused: {r.Error}");
}

if (Port.Dropped.Values.Any(n => n != 0)) throw new Exception("a valid generated call was dropped");

// The holes .NET leaves (concept 3.4): default for a bounded number, null! for an enum. Refused and counted.
if (Port.Add(Names.CheckoutCharges, labels with { Shard = default }).IsSuccess) throw new Exception("default accepted");
if (Port.Add(Names.CheckoutCharges, labels with { Provider = null! }).IsSuccess) throw new Exception("null enum accepted");
// A value made by reflection (the .NET analogue of Go's unsafe) is refused by the table's allowed values.
var visa = (Provider)typeof(Provider).GetConstructor(BindingFlags.NonPublic | BindingFlags.Instance, [typeof(string)])!.Invoke(["visa"]);
var forgedValue = Port.Add(Names.CheckoutCharges, labels with { Provider = visa });
if (forgedValue.IsSuccess || Port.Dropped["unknown_value"] != 1) throw new Exception("a forged enum value was accepted");
Console.WriteLine($"guard-value: {forgedValue.Error}");

// The port reads the tables it was built from: a port built from tables without the signal refuses it.
Port.Install(new Dictionary<string, DurianSignalMetricInfo>(), new Dictionary<string, string>());
var refused = Log.Error(Events.ChargeFailed);
if (refused.IsSuccess || Port.Add(Names.CheckoutCharges, labels).IsSuccess || Port.Dropped["unknown_event"] != 1 || Port.Dropped["unknown_metric"] != 1)
{
    throw new Exception("the port did not refuse a signal missing from its tables");
}

Console.WriteLine($"guard: {refused.Error}");
