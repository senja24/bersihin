/** What the Go side accepts as a run id (internal/progress). */
export const RUN_ID_PATTERN = /^[A-Za-z0-9_-]{16,64}$/;

/**
 * A fresh random run id: 16 random bytes as base64url (22 characters). Not crypto.randomUUID, which needs a
 * secure context and the mygo:// page may not be one.
 */
export function newRunId(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
