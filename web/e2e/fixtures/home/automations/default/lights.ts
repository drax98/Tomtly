// Says the lamp should be on, so a practice run has something to report and going live has something to do.
const lamp = home.device("fake-home:lamp");

home.reconcile(() => {
  lamp.set("switch", true);
});
