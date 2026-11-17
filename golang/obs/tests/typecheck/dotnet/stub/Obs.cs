// Minimal assumed interfaces API from concepts/observability.md §3.4 and goals/lib/dotnet-interfaces.md.
// NOT the real library. Constructors are internal; only the Signals assembly is a friend.
#nullable enable
#pragma warning disable CS1591
using System;
using System.Collections.Generic;
using System.Globalization;
using System.Runtime.CompilerServices;
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

/// <summary>The result of making a bounded number: a value, or the reason it was refused.</summary>
public abstract record Result<T>
{
    private Result()
    {
    }

    public sealed record Ok(T Value) : Result<T>;

    public sealed record Err(string Error) : Result<T>;
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

    public static Result<Int<TSpec>> Of(long n) =>
        n < TSpec.Min || n > TSpec.Max
            ? new Result<Int<TSpec>>.Err(
                $"{TSpec.Name}: {n} is outside {TSpec.Min}..{TSpec.Max}")
            : new Result<Int<TSpec>>.Ok(new Int<TSpec>(n));

    public static Result<Int<TSpec>> AtTime(DateTimeOffset t) =>
        TSpec.Unit switch
        {
            "hour_of_day" => Of(t.Hour),
            "weekday" => Of((int)t.DayOfWeek),
            _ => new Result<Int<TSpec>>.Err(
                $"{TSpec.Name}: unit '{TSpec.Unit}' is not a time bucket"),
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
public sealed record DurianSignalMetricInfo(
    string Kind,
    string Unit,
    long Budget,
    string AcrossPods,
    bool PerPod,
    IReadOnlyList<string> Keys);

/// <summary>One row of SignalTables.Spans.</summary>
public sealed record DurianSignalSpanInfo(string Kind, IReadOnlyList<string> Keys);

public readonly struct TraceLevel : IDurianSignalLevel { public static string Level => "trace"; }
public readonly struct DebugLevel : IDurianSignalLevel { public static string Level => "debug"; }
public readonly struct InfoLevel : IDurianSignalLevel { public static string Level => "info"; }
public readonly struct WarnLevel : IDurianSignalLevel { public static string Level => "warn"; }
public readonly struct ErrorLevel : IDurianSignalLevel { public static string Level => "error"; }
public readonly struct FatalLevel : IDurianSignalLevel { public static string Level => "fatal"; }

public static class Port
{
    public static Result<bool> Add<L>(DurianSignalCounter<L> name, L labels) where L : IDurianSignalLabels
    {
        if (name is null || labels is null) return new Result<bool>.Err("unset signal");
        foreach (var property in typeof(L).GetProperties())
        {
            var value = property.GetValue(labels);
            if (value is null || value is IDurianSignalWire wire && !wire.Valid) return new Result<bool>.Err("unset label");
        }
        return new Result<bool>.Ok(true);
    }
    public static Result<bool> Record<L>(DurianSignalHistogram<L> name, L labels) where L : IDurianSignalLabels => new Result<bool>.Ok(true);
    public static Result<bool> Set<L>(DurianSignalGauge<L> name, L labels) where L : IDurianSignalLabels => new Result<bool>.Ok(true);
}
public static class Log
{
    public static Result<bool> Error(Event<ErrorLevel> ev, IReadOnlyDictionary<string, object>? fields = null, Exception? error = null) => new Result<bool>.Ok(true);
}
