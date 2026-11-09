// Not in any config.json: the page adds it by its folder.
defineExtension({
  id: "folder-plugin",
  name: "Folder Plugin",
  version: "1.0.0",
  description: "Added from a folder in the config folder.",
  data: { everySeconds: 60, fetch: () => ({ from: "folder" }) },
});
