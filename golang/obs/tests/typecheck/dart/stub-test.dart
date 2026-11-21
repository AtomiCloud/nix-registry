// ignore_for_file: avoid_print
import 'stub/lib/diene_interfaces.dart';

final class SpaceKeys extends DurianSignalLabels {
  const SpaceKeys();
  @override
  Map<String, Object> get entries => {'a b': 'value'};
}

final class SpaceSpan extends DurianSignalSpan<SpaceKeys> {
  const SpaceSpan() : super('space.keys', 'internal');
}

void main() {
  final installed = DurianSignalPort.install(
    contract: 'space:contract.yaml',
    hash: 'x',
    metrics: {},
    events: {},
    spans: {
      'space.keys': const DurianSignalSpanInfo(
        kind: 'internal',
        keys: ['a', 'b'],
      ),
    },
  );
  if (installed is! DurianSignalOk<DurianSignalPort>)
    throw StateError('install');
  final result = DurianSignalPort.startSpan(
    const SpaceSpan(),
    const SpaceKeys(),
  );
  if (result is! DurianSignalErr<bool>)
    throw StateError('space-joined attribute keys accepted');
  print('PASS stub span compares exact key lists');
}
