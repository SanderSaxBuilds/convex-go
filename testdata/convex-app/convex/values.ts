import { queryGeneric as query } from "convex/server";

// Returns its arguments unchanged, so the client can check that every
// Convex type survives a trip through a real deployment.
export const echo = query({
  handler: async (_ctx, args) => args,
});

// Describes the JavaScript type each argument arrived as.
export const describe = query({
  handler: async (_ctx, args: Record<string, unknown>) => {
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(args)) {
      if (v === null) out[k] = "null";
      else if (v instanceof ArrayBuffer) out[k] = "ArrayBuffer";
      else if (Array.isArray(v)) out[k] = "array";
      else if (typeof v === "number" && Number.isNaN(v)) out[k] = "NaN";
      else if (typeof v === "number" && Object.is(v, -0)) out[k] = "-0";
      else out[k] = typeof v;
    }
    return out;
  },
});

export const special = query({
  handler: async () => ({
    int: 9007199254740993n,
    float: 1.5,
    bytes: new Uint8Array([0, 255, 16]).buffer,
    inf: Infinity,
    negInf: -Infinity,
    negZero: -0,
    nan: NaN,
  }),
});
