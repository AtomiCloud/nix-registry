using System.Globalization;
using AtomiCloud.Diene.Interfaces;

var contract = new DurianSignalContract("ordinal:contract.yaml", "x");
var metrics = new Dictionary<string, DurianSignalMetricInfo>();
var spans = new Dictionary<string, DurianSignalSpanInfo>();
var events = new Dictionary<string, string> { ["sort.I"] = "error", ["sort.i"] = "error", ["sort.ı"] = "error", ["sort.İ"] = "error" };
CultureInfo.CurrentCulture = CultureInfo.GetCultureInfo("en-US");
if (!Port.Install(contract, metrics, events, spans).IsSuccess) throw new Exception("first install");
CultureInfo.CurrentCulture = CultureInfo.GetCultureInfo("tr-TR");
var reverse = events.Reverse().ToDictionary(x => x.Key, x => x.Value);
if (!Port.Install(contract, metrics, reverse, spans).IsSuccess) throw new Exception("repeat changed with culture");
Console.WriteLine("PASS stub registration uses ordinal ordering across cultures");
