// Maps knowledge staff API errors to admin locale keys. Branch on status and
// code only; never surface problem titles (English, for developers) to staff.
import { ApiError } from '@birge/api';

/** KnowledgeProblemKey is a locale suffix under knowledge.problem.*. */
export type KnowledgeProblemKey = 'forbidden' | 'conflict' | 'failed';

/** knowledgeProblemKey picks the locale key for a knowledge mutation/list error. */
export function knowledgeProblemKey(err: unknown): KnowledgeProblemKey {
  if (err instanceof ApiError) {
    if (err.status === 403 || err.code === 'forbidden') return 'forbidden';
    if (err.status === 409 || err.code === 'conflict') return 'conflict';
  }
  return 'failed';
}
