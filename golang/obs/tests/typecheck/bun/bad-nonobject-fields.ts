import { logger } from '@atomicloud/diene.interfaces';
import { ChargeFailed, eventTable } from './signals';
logger(eventTable).error(ChargeFailed, 42);
