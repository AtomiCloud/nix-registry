// planted (review item 3): upcast a generated metric to its base and add the wrong labels class. There is
// no generic add on the base, so this is a compile error, not a run-time TypeError.
import 'package:checkout_signals/src/signals/signals.g.dart';
void main() {
  final DurianSignalCounter<DurianSignalLabels> c = checkoutCharges;
  c.add(const CheckoutChargeDurationLabels(provider: Provider.stripe, outcome: Outcome.ok));
}
