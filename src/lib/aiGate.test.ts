import { describe, expect, test } from "bun:test";
import { createAiGate } from "./aiGate";
import type { AiContext } from "../types";

const context: AiContext = { largeFiles: [{ path: "~/a.zip", size: 1, modified: 1 }] };

function setup() {
  const calls: { question: string; context: AiContext }[] = [];
  const gate = createAiGate(async (question: string, ctx: AiContext) => {
    calls.push({ question, context: ctx });
    return "answer";
  });
  return { calls, gate };
}

describe("createAiGate", () => {
  test("askAi is not called before the preview's Send", async () => {
    const { calls, gate } = setup();
    const out = await gate.submit("q", context, "claude");
    expect(out.status).toBe("preview");
    expect(calls).toHaveLength(0);
  });

  test("Send passes exactly the previewed context and question", async () => {
    const { calls, gate } = setup();
    const out = await gate.submit("q", context, "claude");
    if (out.status !== "preview") throw new Error("expected a preview");
    await gate.confirm(out.pending);
    expect(calls).toHaveLength(1);
    expect(calls[0]!.context).toBe(out.pending.context);
    expect(calls[0]!.question).toBe("q");
  });

  test("after Send, later questions to the same vendor go out directly", async () => {
    const { calls, gate } = setup();
    const first = await gate.submit("q", context, "claude");
    if (first.status !== "preview") throw new Error("expected a preview");
    await gate.confirm(first.pending);
    expect((await gate.submit("again", context, "claude")).status).toBe("sent");
    expect(calls).toHaveLength(2);
  });

  test("a vendor switch needs a new preview", async () => {
    const { calls, gate } = setup();
    const first = await gate.submit("q", context, "claude");
    if (first.status !== "preview") throw new Error("expected a preview");
    await gate.confirm(first.pending);
    const second = await gate.submit("again", context, "chatgpt");
    expect(second.status).toBe("preview");
    expect(calls).toHaveLength(1);
  });
});
