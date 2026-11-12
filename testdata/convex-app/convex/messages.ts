import {
  actionGeneric as action,
  internalQueryGeneric as internalQuery,
  mutationGeneric as mutation,
  queryGeneric as query,
} from "convex/server";
import { ConvexError, v } from "convex/values";

export const send = mutation({
  args: { author: v.string(), body: v.string() },
  handler: async (ctx, args) => {
    if (args.body.length > 100) {
      throw new ConvexError({ code: "TOO_LONG", max: 100n });
    }
    return await ctx.db.insert("messages", args);
  },
});

export const list = query({
  args: { author: v.optional(v.string()) },
  handler: async (ctx, args) => {
    const all = await ctx.db.query("messages").collect();
    return all.filter((m) => args.author === undefined || m.author === args.author);
  },
});

export const shout = action({
  args: { text: v.string() },
  handler: async (_ctx, args) => {
    console.log("shouting", args.text);
    return args.text.toUpperCase();
  },
});

export const fail = query({
  handler: async () => {
    throw new Error("boom");
  },
});

export const failWithString = mutation({
  handler: async () => {
    throw new ConvexError("plain message");
  },
});

export const whoami = query({
  handler: async (ctx) => {
    const identity = await ctx.auth.getUserIdentity();
    return identity === null ? null : identity.subject;
  },
});

export const secret = internalQuery({
  handler: async () => "internal only",
});
