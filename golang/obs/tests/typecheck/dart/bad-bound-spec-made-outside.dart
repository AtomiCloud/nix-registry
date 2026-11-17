// planted: an app makes its own, wider range spec. The library spec class is abstract: only the generated
// subclass can be made.
import 'package:checkout_signals/src/signals/signals.g.dart';
const s = DurianSignalBoundSpec<Shard>('shard', 0, 1000000, '', Shard.new);
