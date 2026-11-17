// planted (review repro): an app class implements the library's Event with a typo name and a wrong level.
import 'package:checkout_signals/src/signals/signals.g.dart';
final class ForgedEvent implements Event<ErrorLevel> {
  const ForgedEvent();
  @override
  String get name => 'checkout.chrage.faild';
  @override
  String get level => 'info';
}
