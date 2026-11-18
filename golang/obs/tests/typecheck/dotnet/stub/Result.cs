// Minimal assumed stand-in for the AtomiCloud.Diene.Result package (goals/lib/dotnet-result.md: a rich
// struct Result<T, E> with Ok/Err factories and IsSuccess). The goal leaves the namespace layout and the
// member set open: the namespace AtomiCloud.Diene.Result and Value/Error are assumed, not in the goal.
#nullable enable
#pragma warning disable CS1591
using System;
namespace AtomiCloud.Diene.Result;

public readonly struct Result<T, E>
{
    private readonly T? _value;
    private readonly E? _error;

    private Result(bool ok, T? value, E? error)
    {
        IsSuccess = ok;
        _value = value;
        _error = error;
    }

    public bool IsSuccess { get; }

    public T Value => IsSuccess ? _value! : throw new InvalidOperationException("not a success");

    public E Error => !IsSuccess ? _error! : throw new InvalidOperationException("not a failure");

    public static Result<T, E> Ok(T value) => new(true, value, default);

    public static Result<T, E> Err(E error) => new(false, default, error);
}
