// Minimal assumed interfaces API from concepts/observability.md §3.4 and goals/lib/dotnet-interfaces.md, with
// enough body to RUN the good consumer. NOT the real library. Constructors are internal; only the Signals
// assembly is a friend (that closes forging at compile time, measured by the bad-*-made-outside consumers).
// The goal spells Port.Add, Int<Spec>.Of returning a Result, Log.Error(Event<ErrorLevel>, ...), sealed enum
// classes and internal carrier constructors; every other name here (the IDurianSignal* interfaces, Valid/Wire,
// AtTime, DurianSignalDate, the table rows, Port.Install, Dropped) is assumed, not in the goal.
#nullable enable
#pragma warning disable CS1591
using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Runtime.CompilerServices;
using AtomiCloud.Diene.Result;
[assembly: InternalsVisibleTo("Signals")]
namespace AtomiCloud.Diene.Interfaces;
/// <summary>A label value that has a wire form. Valid is false for default(...) of a bounded number or date.</summary>
public interface IDurianSignalWire
{
    string Wire { get; }

    bool Valid { get; }
}

/// <summary>Implemented by the generated labels and attributes records only.</summary>
public interface IDurianSignalLabels
{
}

/// <summary>Implemented by the level markers only.</summary>
public interface IDurianSignalLevel
{
    static abstract string Level { get; }
}

/// <summary>The range of a bounded number or time bucket.</summary>
public interface IDurianSignalIntSpec
{
    static abstract string Name { get; }

    static abstract long Min { get; }

    static abstract long Max { get; }

    static abstract string Unit { get; }
}

/// <summary>The window of a calendar date label.</summary>
public interface IDurianSignalDateSpec
{
    static abstract string Name { get; }

    static abstract long WindowDays { get; }
}

/// <summary>The wire name of a labels or attributes property.</summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class DurianSignalLabelAttribute : Attribute
{
    public DurianSignalLabelAttribute(string name)
    {
        Name = name;
    }

    public string Name { get; }
}

/// <summary>A bounded number or time bucket. Of and AtTime are the only ways to make a valid one.</summary>
public readonly struct Int<TSpec> : IDurianSignalWire
    where TSpec : IDurianSignalIntSpec
{
    private readonly long _value;
    private readonly bool _valid;

    private Int(long value)
    {
        _value = value;
        _valid = true;
    }

    public bool Valid => _valid;

    public long Value => _value;

    public string Wire => _valid ? _value.ToString(CultureInfo.InvariantCulture) : string.Empty;

    public static Result<Int<TSpec>, string> Of(long n) =>
        n < TSpec.Min || n > TSpec.Max
            ? Result<Int<TSpec>, string>.Err($"{TSpec.Name}: {n} is outside {TSpec.Min}..{TSpec.Max}")
            : Result<Int<TSpec>, string>.Ok(new Int<TSpec>(n));

    public static Result<Int<TSpec>, string> AtTime(DateTimeOffset t) =>
        TSpec.Unit switch
        {
            "hour_of_day" => Of(t.Hour),
            "weekday" => Of((int)t.DayOfWeek),
            _ => Result<Int<TSpec>, string>.Err($"{TSpec.Name}: unit '{TSpec.Unit}' is not a time bucket"),
        };

    public override string ToString() => Wire;
}

/// <summary>A calendar date label (UTC, yyyy-MM-dd). Of is the only way to make a valid one.</summary>
public readonly struct DurianSignalDate<TSpec> : IDurianSignalWire
    where TSpec : IDurianSignalDateSpec
{
    private readonly string? _wire;

    private DurianSignalDate(string wire)
    {
        _wire = wire;
    }

    public bool Valid => _wire is not null;

    public string Wire => _wire ?? string.Empty;

    public static DurianSignalDate<TSpec> Of(DateOnly date) =>
        new(date.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture));

    public static DurianSignalDate<TSpec> Of(DateTimeOffset t) => Of(DateOnly.FromDateTime(t.UtcDateTime));

    public override string ToString() => Wire;
}

/// <summary>A log event: a name carrier whose type includes its level. Only this assembly can make one.</summary>
public sealed class Event<TLevel>
    where TLevel : IDurianSignalLevel
{
    internal Event(string name)
    {
        Name = name;
    }

    /// <summary>The event name (the value of the durian.cloud/event field).</summary>
    public string Name { get; }

    /// <summary>The level the type fixes.</summary>
    public string Level => TLevel.Level;
}

/// <summary>A counter name carrier; TLabels is its labels record.</summary>
public sealed class DurianSignalCounter<TLabels>
    where TLabels : IDurianSignalLabels
{
    internal DurianSignalCounter(string name)
    {
        Name = name;
    }

    public string Name { get; }
}

/// <summary>A histogram name carrier.</summary>
public sealed class DurianSignalHistogram<TLabels>
    where TLabels : IDurianSignalLabels
{
    internal DurianSignalHistogram(string name)
    {
        Name = name;
    }

    public string Name { get; }
}

/// <summary>A gauge name carrier.</summary>
public sealed class DurianSignalGauge<TLabels>
    where TLabels : IDurianSignalLabels
{
    internal DurianSignalGauge(string name)
    {
        Name = name;
    }

    public string Name { get; }
}

/// <summary>A span name carrier; TAttributes is its attributes record.</summary>
public sealed class DurianSignalSpan<TAttributes>
    where TAttributes : IDurianSignalLabels
{
    internal DurianSignalSpan(string name, string kind)
    {
        Name = name;
        Kind = kind;
    }

    public string Name { get; }

    /// <summary>The span kind: internal, client, server, producer or consumer.</summary>
    public string Kind { get; }
}

/// <summary>One row of SignalTables.Metrics.</summary>
/// <param name="Kind">counter, histogram or gauge.</param>
/// <param name="Unit">The stored name suffix (seconds, bytes), or empty.</param>
/// <param name="Budget">The most series the metric may store per landscape (0: none stated).</param>
/// <param name="AcrossPods">sum, avg, max or last; empty for counters, histograms and per-pod gauges.</param>
/// <param name="PerPod">True for a gauge that keeps its pod keys.</param>
/// <param name="Keys">The allowed label keys, in contract order.</param>
/// <param name="Values">The allowed wire values per enum label.</param>
/// <param name="Ranges">The [min, max] per bounded-number label.</param>
public sealed record DurianSignalMetricInfo(
    string Kind,
    string Unit,
    long Budget,
    string AcrossPods,
    bool PerPod,
    IReadOnlyList<string> Keys,
    IReadOnlyDictionary<string, string[]>? Values = null,
    IReadOnlyDictionary<string, long[]>? Ranges = null);

/// <summary>One row of SignalTables.Spans.</summary>
public sealed record DurianSignalSpanInfo(string Kind, IReadOnlyList<string> Keys);

public readonly struct TraceLevel : IDurianSignalLevel { public static string Level => "trace"; }
public readonly struct DebugLevel : IDurianSignalLevel { public static string Level => "debug"; }
public readonly struct InfoLevel : IDurianSignalLevel { public static string Level => "info"; }
public readonly struct WarnLevel : IDurianSignalLevel { public static string Level => "warn"; }
public readonly struct ErrorLevel : IDurianSignalLevel { public static string Level => "error"; }
public readonly struct FatalLevel : IDurianSignalLevel { public static string Level => "fatal"; }

/// <summary>The telemetry port, built from the generated tables (Port.Install). A refusal is an Err and a
/// count in Dropped; it never throws and never changes the app's result.</summary>
public sealed record DurianSignalContract(string Id, string Hash);

public static class Port
{
    private static IReadOnlyDictionary<string, DurianSignalMetricInfo> _metrics = new Dictionary<string, DurianSignalMetricInfo>();
    private static IReadOnlyDictionary<string, string> _events = new Dictionary<string, string>();

    /// <summary>dropped.&lt;reason&gt;: unknown_event, unknown_metric, unknown_key, unknown_value, unset_label.</summary>
    public static Dictionary<string, int> Dropped { get; } = new();

    /// <summary>Merges a deep copy per contract at boot, before signals flow.</summary>
    private static readonly Dictionary<string, string> Contracts = new();
    private static readonly Dictionary<string, string> Owners = new();
    private static readonly Dictionary<string, DurianSignalSpanInfo> Spans = new();
    private static bool _closed, _poisoned;

    public static Result<bool, string> Install(DurianSignalContract contract,
        IReadOnlyDictionary<string, DurianSignalMetricInfo> metrics,
        IReadOnlyDictionary<string, string> eventLevels,
        IReadOnlyDictionary<string, DurianSignalSpanInfo> spans)
    {
        Result<bool, string> Refuse(string why) { _poisoned = true; return Drop("register", why); }
        try
        {
            if (_poisoned) return Refuse($"contract {contract.Id}: the port is already poisoned");
            if (_closed) return Refuse($"contract {contract.Id} registered after the first signal");
            var copiedMetrics = metrics.ToDictionary(x => x.Key, x => x.Value with
            {
                Keys = x.Value.Keys.ToArray(),
                Values = x.Value.Values?.ToDictionary(v => v.Key, v => v.Value.ToArray()),
                Ranges = x.Value.Ranges?.ToDictionary(v => v.Key, v => v.Value.ToArray())
            });
            var copiedEvents = eventLevels.ToDictionary(x => x.Key, x => x.Value);
            var copiedSpans = spans.ToDictionary(x => x.Key, x => x.Value with { Keys = x.Value.Keys.ToArray() });
            var fingerprint = System.Text.Json.JsonSerializer.Serialize(new
            {
                contract.Hash,
                Metrics = copiedMetrics.OrderBy(x => x.Key).Select(x => new
                {
                    x.Key,
                    x.Value.Kind,
                    x.Value.Unit,
                    x.Value.Budget,
                    x.Value.AcrossPods,
                    x.Value.PerPod,
                    x.Value.Keys,
                    Values = x.Value.Values?.OrderBy(v => v.Key).Select(v => new { v.Key, v.Value }),
                    Ranges = x.Value.Ranges?.OrderBy(v => v.Key).Select(v => new { v.Key, v.Value })
                }),
                Events = copiedEvents.OrderBy(x => x.Key),
                Spans = copiedSpans.OrderBy(x => x.Key)
            });
            if (Contracts.TryGetValue(contract.Id, out var previous))
                return previous == fingerprint ? Result<bool, string>.Ok(true) : Refuse($"contract {contract.Id} reused with a different hash or table");
            var names = metrics.Keys.Concat(eventLevels.Keys).Concat(spans.Keys).Distinct().ToArray();
            foreach (var name in names) if (Owners.TryGetValue(name, out var owner))
                return Refuse($"signal {name} is owned by both contract {owner} and contract {contract.Id}");
            Contracts[contract.Id] = fingerprint;
            foreach (var name in names) Owners[name] = contract.Id;
            foreach (var row in copiedMetrics) ((Dictionary<string, DurianSignalMetricInfo>)_metrics).Add(row.Key, row.Value);
            foreach (var row in copiedEvents) ((Dictionary<string, string>)_events).Add(row.Key, row.Value);
            foreach (var row in copiedSpans) Spans.Add(row.Key, row.Value);
            return Result<bool, string>.Ok(true);
        }
        catch (Exception) { return Refuse("invalid registration table contents"); }
    }

    internal static Result<bool, string> Drop(string reason, string why)
    {
        Dropped[reason] = Dropped.GetValueOrDefault(reason) + 1;
        return Result<bool, string>.Err($"dropped.{reason}: {why}");
    }

    public static Result<bool, string> Add<L>(DurianSignalCounter<L> name, L labels) where L : IDurianSignalLabels => Emit("counter", name?.Name, labels);
    public static Result<bool, string> Record<L>(DurianSignalHistogram<L> name, L labels) where L : IDurianSignalLabels => Emit("histogram", name?.Name, labels);
    public static Result<bool, string> Set<L>(DurianSignalGauge<L> name, L labels) where L : IDurianSignalLabels => Emit("gauge", name?.Name, labels);

    private static Result<bool, string> Emit<L>(string kind, string? name, L labels) where L : IDurianSignalLabels
    {
        if (_poisoned) return Drop("poisoned", "the port is poisoned until restart");
        if (name is null || labels is null) return Drop("unknown_metric", "unset signal");
        if (!_metrics.TryGetValue(name, out var info)) return Drop("unknown_metric", $"metric \"{name}\" is not in the generated metric table");
        if (info.Kind != kind) return Drop("unknown_metric", $"metric \"{name}\" is a {info.Kind} in the generated metric table, not a {kind}");
        var props = typeof(L).GetProperties();
        var keys = new List<string>();
        foreach (var p in props)
        {
            keys.Add(((DurianSignalLabelAttribute)Attribute.GetCustomAttribute(p, typeof(DurianSignalLabelAttribute))!).Name);
        }
        if (!keys.SequenceEqual(info.Keys)) return Drop("unknown_key", $"metric \"{name}\" labels [{string.Join(' ', keys)}] are not its generated keys [{string.Join(' ', info.Keys)}]");
        foreach (var p in props)
        {
            var value = p.GetValue(labels);
            if (value is null || value is IDurianSignalWire wire && !wire.Valid) return Drop("unset_label", $"metric \"{name}\" label {p.Name} is unset");
            var key = ((DurianSignalLabelAttribute)Attribute.GetCustomAttribute(p, typeof(DurianSignalLabelAttribute))!).Name;
            var text = value is IDurianSignalWire w ? w.Wire : value.ToString() ?? string.Empty;
            if (info.Values is not null && info.Values.TryGetValue(key, out var allowed) && !allowed.Contains(text))
                return Drop("unknown_value", $"metric \"{name}\" label {key} value \"{text}\" is not one of [{string.Join(' ', allowed)}]");
            if (info.Ranges is not null && info.Ranges.TryGetValue(key, out var range) &&
                (!long.TryParse(text, System.Globalization.NumberStyles.Integer, System.Globalization.CultureInfo.InvariantCulture, out var n) || n < range[0] || n > range[1]))
                return Drop("unknown_value", $"metric \"{name}\" label {key} value {text} is outside {range[0]}..{range[1]}");
        }
        _closed = true;
        return Result<bool, string>.Ok(true);
    }

    internal static Result<bool, string> LogEvent(string level, string? name)
    {
        if (_poisoned) return Drop("poisoned", "the port is poisoned until restart");
        if (name is null) return Drop("unknown_event", "unset event");
        if (!_events.TryGetValue(name, out var want)) return Drop("unknown_event", $"event \"{name}\" is not in the generated event table");
        if (want != level) return Drop("unknown_event", $"event \"{name}\" is {want} in the generated event table, not {level}");
        _closed = true;
        return Result<bool, string>.Ok(true);
    }
}
public static class Log
{
    public static Result<bool, string> Error(Event<ErrorLevel> ev, IReadOnlyDictionary<string, object>? fields = null, Exception? error = null) => Port.LogEvent("error", ev?.Name);
    public static Result<bool, string> Warn(Event<WarnLevel> ev, IReadOnlyDictionary<string, object>? fields = null) => Port.LogEvent("warn", ev?.Name);
}
