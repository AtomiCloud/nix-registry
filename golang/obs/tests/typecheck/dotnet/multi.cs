using AtomiCloud.Diene.Interfaces;
using CheckoutSignals = Checkout.Signals;
using BillingSignals = Billing.Signals;

void Check(bool ok, object why){if(!ok)throw new Exception(why.ToString());}
var scenario=args[0];
if(scenario=="early-real-forgery"){
 Check(Port.Install(new("forged:contract.yaml","x"),new Dictionary<string,DurianSignalMetricInfo>(),new Dictionary<string,string>{{"checkout.charge.failed","error"}},new Dictionary<string,DurianSignalSpanInfo>()).IsSuccess,"early install");
 var refused=Port.Install(CheckoutSignals.SignalTables.Contract,CheckoutSignals.SignalTables.Metrics,CheckoutSignals.SignalTables.EventLevels,CheckoutSignals.SignalTables.Spans);
 Check(!refused.IsSuccess&&refused.Error.Contains("forged:contract.yaml",StringComparison.Ordinal)&&refused.Error.Contains("checkout.Signals:observability/contract.yaml",StringComparison.Ordinal),"both ids in error");
 Check(!Log.Error(CheckoutSignals.Events.ChargeFailed).IsSuccess&&Port.Dropped["register"]==1&&Port.Dropped["poisoned"]==1,"poison counts");
 Console.WriteLine("PASS multi early-real-forgery");return;
}
var contract=CheckoutSignals.SignalTables.Contract;
IReadOnlyDictionary<string,DurianSignalMetricInfo> metrics=CheckoutSignals.SignalTables.Metrics;
IReadOnlyDictionary<string,string> events=CheckoutSignals.SignalTables.EventLevels;
IReadOnlyDictionary<string,DurianSignalSpanInfo> spans=CheckoutSignals.SignalTables.Spans;
Check(Port.Install(contract,metrics,events,spans).IsSuccess,"checkout install");
Check(Port.Install(BillingSignals.SignalTables.Contract,BillingSignals.SignalTables.Metrics,BillingSignals.SignalTables.EventLevels,BillingSignals.SignalTables.Spans).IsSuccess,"billing install");
switch(scenario){
 case "merged":
  Check(Log.Error(CheckoutSignals.Events.ChargeFailed).IsSuccess,"checkout event");Check(Log.Error(BillingSignals.Events.ChargeFailed).IsSuccess,"billing event");
  Check(Port.Add(CheckoutSignals.Names.CheckoutInvoices,new CheckoutSignals.CheckoutInvoicesLabels(DurianSignalDate<CheckoutSignals.BillingDaySpec>.Of(new DateOnly(2026,11,17)),CheckoutSignals.Provider.Stripe)).IsSuccess,"checkout metric");
  Check(Port.Add(BillingSignals.Names.BillingCharges,new BillingSignals.BillingChargesLabels(BillingSignals.Provider.Stripe)).IsSuccess,"billing metric");break;
 case "repeat":Check(Port.Install(contract,metrics,events,spans).IsSuccess,"same id/hash/content no-op");break;
 case "changed-hash":contract=contract with {Hash="changed"};break;
 case "changed-table":events=new Dictionary<string,string>{{"other.event","error"}};break;
 case "duplicate-metric":contract=new("other:contract.yaml","x");metrics=new Dictionary<string,DurianSignalMetricInfo>{{"checkout.charges",CheckoutSignals.SignalTables.Metrics["checkout.charges"]}};events=new Dictionary<string,string>();spans=new Dictionary<string,DurianSignalSpanInfo>();break;
 case "duplicate-event":contract=new("other:contract.yaml","x");metrics=new Dictionary<string,DurianSignalMetricInfo>();events=new Dictionary<string,string>{{"checkout.charge.failed","error"}};spans=new Dictionary<string,DurianSignalSpanInfo>();break;
 case "duplicate-span":contract=new("other:contract.yaml","x");metrics=new Dictionary<string,DurianSignalMetricInfo>();events=new Dictionary<string,string>();spans=new Dictionary<string,DurianSignalSpanInfo>{{"checkout.charge",CheckoutSignals.SignalTables.Spans["checkout.charge"]}};break;
 case "cross-kind":contract=new("other:contract.yaml","x");metrics=new Dictionary<string,DurianSignalMetricInfo>();events=new Dictionary<string,string>{{"checkout.charge","error"}};spans=new Dictionary<string,DurianSignalSpanInfo>();break;
 case "late":Check(Log.Error(BillingSignals.Events.ChargeFailed).IsSuccess,"first signal");contract=new("late:contract.yaml","x");metrics=new Dictionary<string,DurianSignalMetricInfo>();events=new Dictionary<string,string>();spans=new Dictionary<string,DurianSignalSpanInfo>();break;
 case "fresh-forgery":
  Check(Port.Install(new("forged:contract.yaml","x"),new Dictionary<string,DurianSignalMetricInfo>(),new Dictionary<string,string>{{"forged.fresh","error"}},new Dictionary<string,DurianSignalSpanInfo>()).IsSuccess,"fresh names admitted");
  // Reflection is the remaining .NET escape hatch; no write-door guard covers this event.
  var forged=(Event<ErrorLevel>)typeof(Event<ErrorLevel>).GetConstructor(System.Reflection.BindingFlags.NonPublic|System.Reflection.BindingFlags.Instance,[typeof(string)])!.Invoke(["forged.fresh"]);
  Check(Log.Error(forged).IsSuccess,"fresh forgery admitted: lint only");break;
}
if(!new[]{"merged","repeat","fresh-forgery"}.Contains(scenario)){
 var r=Port.Install(contract,metrics,events,spans);Check(!r.IsSuccess,"registration accepted");
 if(scenario.StartsWith("duplicate",StringComparison.Ordinal)||scenario=="cross-kind")Check(r.Error.Contains("checkout.Signals:observability/contract.yaml",StringComparison.Ordinal)&&r.Error.Contains("other:contract.yaml",StringComparison.Ordinal),r.Error);
 Check(!Log.Error(CheckoutSignals.Events.ChargeFailed).IsSuccess,"must poison checkout");Check(!Log.Error(BillingSignals.Events.ChargeFailed).IsSuccess,"must poison billing");
 Check(Port.Dropped["register"]==1&&Port.Dropped["poisoned"]==2,"counts");
}
Console.WriteLine($"PASS multi {scenario}");
