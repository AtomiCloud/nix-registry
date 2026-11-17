// planted: an app class implements a library metric carrier (skipping its constructor).
import 'package:checkout_signals/src/signals/signals.g.dart';
final class ForgedMetric implements DurianSignalCounter<CheckoutChargesLabels> {
  @override
  String get name => 'not.in.contract';
}
