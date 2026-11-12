import { queryGeneric as query } from "convex/server";

export const where = query({
  handler: async () => "folder/nested:where",
});

export default query({
  handler: async () => "folder/nested:default",
});
