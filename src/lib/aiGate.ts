import type { AiContext } from "../types";
import { needsPreview } from "./aiPreview";

export interface Pending {
  question: string;
  context: AiContext;
  vendor: string;
}

/**
 * Holds every question until the user has seen exactly what goes out. `askAi` is only reached through
 * `confirm` (with the very context that was previewed) or, after a confirmation for the same vendor, `submit`.
 */
export function createAiGate<A>(askAi: (question: string, context: AiContext) => Promise<A>) {
  let approvedFor: string | null = null;
  return {
    async submit(question: string, context: AiContext, vendor: string): Promise<{ status: "preview"; pending: Pending } | { status: "sent"; answer: A }> {
      if (needsPreview(approvedFor, vendor)) return { status: "preview", pending: { question, context, vendor } };
      return { status: "sent", answer: await askAi(question, context) };
    },
    async confirm(pending: Pending): Promise<A> {
      approvedFor = pending.vendor;
      return askAi(pending.question, pending.context);
    },
  };
}
