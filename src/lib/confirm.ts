/** The refusals Go gives when a destructive call is not backed by a valid token or scan (internal/api). */
// MyGo rejects with a CallError whose message is the Go error text. A Go method that wraps the error would add a prefix,
// so the text is looked for inside the message. None of these texts contains "cancel".
const messageIs = (err: unknown, ...texts: string[]) => err instanceof Error && texts.some((t) => err.message.includes(t));

/** The confirmation token was missing, wrong, used up or too old: the user has to confirm again. */
export const isConfirmError = (err: unknown) => messageIs(err, "Confirmation required", "Confirmation expired");

/** The scan the ids or paths came from is gone or too old: scan again. */
export const isScanExpired = (err: unknown) => messageIs(err, "Scan expired: scan again");
