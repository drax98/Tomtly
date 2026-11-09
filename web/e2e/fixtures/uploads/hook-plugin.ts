defineExtension({
  id: "hook-plugin",
  name: "Hook Plugin",
  version: "1.0.0",
  description: "Publishes whatever is posted to its webhook.",
  access: { webhooks: true },
  webhooks: {
    receive(ctx, request) {
      ctx.publish({ said: JSON.parse(request.body).said, path: request.path });
      return { status: 202, body: { received: true } };
    },
  },
});
