// planted forging (review round 6), companion module: loaded before signals.ts (see forge-register-first.ts),
// it registers a forged event table first.
import { register } from '@atomicloud/diene.interfaces';
register({ events: { 'checkout.chrage.faild': 'error' } });
