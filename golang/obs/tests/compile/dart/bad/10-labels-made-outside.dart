// expect: no_default_super_constructor
import 'package:checkout_signals/src/signals/signals.g.dart';
final class Evil extends DurianSignalLabels {
  @override
  Map<String, Object> get entries => const {};
}
