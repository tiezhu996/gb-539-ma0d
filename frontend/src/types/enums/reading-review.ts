export const READING_REVIEW_STATES = ['pending', 'adopted', 'excluded'] as const;
export type ReadingReviewState = typeof READING_REVIEW_STATES[number];
