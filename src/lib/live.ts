import type { ScanProgress, ScanTask } from "../types";

/** Latest progress report of each running call, by run id. Insertion order is age: the last key is the newest. */
export type Slots = Record<string, ScanProgress>;

const MAX_SLOTS = 16;

export function applyProgress(slots: Slots, p: ScanProgress): Slots {
  const next: Slots = { ...slots };
  delete next[p.id];
  next[p.id] = p;
  const ids = Object.keys(next);
  for (const id of ids.slice(0, Math.max(0, ids.length - MAX_SLOTS))) delete next[id];
  return next;
}

export function dropRun(slots: Slots, id: string): Slots {
  if (!(id in slots)) return slots;
  const next = { ...slots };
  delete next[id];
  return next;
}

export function dropTask(slots: Slots, task: ScanTask): Slots {
  const keep = Object.entries(slots).filter(([, p]) => p.task !== task);
  return keep.length === Object.keys(slots).length ? slots : Object.fromEntries(keep);
}

/** The newest report among the given tasks, or among all when none are given. */
export function pick(slots: Slots, tasks?: readonly ScanTask[]): ScanProgress | null {
  const ids = Object.keys(slots);
  for (let i = ids.length - 1; i >= 0; i--) {
    const p = slots[ids[i]!]!;
    if (!tasks || tasks.length === 0 || tasks.includes(p.task)) return p;
  }
  return null;
}
