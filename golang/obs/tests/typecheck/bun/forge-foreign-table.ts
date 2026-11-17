// planted forging (review round 6): app code hands the port its own event table (allowing a misspelled
// event) instead of the generated one. The port accepts only the tables the generated signals.ts registered:
// a foreign table poisons the port, which then refuses every signal until restart.
import { ChargeFailed } from './signals';
import { dropped, logger } from '@atomicloud/diene.interfaces';
const log = logger({ 'checkout.chrage.faild': 'error' });
const forged = log.error({ name: 'checkout.chrage.faild', level: 'error' });
const real = log.error(ChargeFailed);
console.log('result:', JSON.stringify(forged));
if (forged.ok || real.ok || dropped('register') !== 1) throw new Error('a foreign table was used');
