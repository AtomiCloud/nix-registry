// planted forging (review round 6): an app module registers a forged table before the generated signals.ts
// loads. The generated registration is then the second one: it poisons the port, which refuses every signal
// from then on, the forged one and the real one alike (a loud telemetry failure, never an app failure).
import './forge-register-first.early';
import { ChargeFailed, eventTable } from './signals';
import { dropped, logger } from '@atomicloud/diene.interfaces';
const log = logger(eventTable);
const forged = log.error({ name: 'checkout.chrage.faild', level: 'error' });
const real = log.error(ChargeFailed);
console.log('result:', JSON.stringify(forged));
if (forged.ok || real.ok || dropped('register') !== 1 || dropped('poisoned') !== 2)
  throw new Error('a registration that ran first was used silently');
