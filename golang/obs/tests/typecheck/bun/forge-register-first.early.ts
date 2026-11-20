// planted forging (review round 6), companion module: loaded before signals.ts (see forge-register-first.ts),
// it registers a forged event table first.
import { register } from '@atomicloud/diene.interfaces';
register({
  contract: 'forged:observability/contract.yaml',
  hash: 'sha256:forged',
  events: { 'checkout.chrage.faild': 'error', 'checkout.charge.failed': 'error' },
});
