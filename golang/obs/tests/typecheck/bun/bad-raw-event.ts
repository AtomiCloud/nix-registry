import { logger } from '@atomicloud/diene.interfaces';
import { eventTable } from './signals';
logger(eventTable).error('checkout.charge.failed');
