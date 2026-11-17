// expect: missing_required_argument
import 'package:checkout_signals/src/signals/signals.g.dart';
final l = CheckoutChargesLabels(provider: Provider.stripe, outcome: Outcome.ok);
