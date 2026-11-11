// A lamp on a smart plug, on a schedule. Using daily rather than reconcile means you can still switch it by hand.
const plug = home.device("lamp-plug");

home.daily("08:30", () => plug.set("switch", true));
home.daily("00:30", () => plug.set("switch", false));
