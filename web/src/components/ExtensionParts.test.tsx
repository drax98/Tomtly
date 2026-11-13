import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type ExtensionStatus } from "@/api";
import { WithRole } from "@/test/helpers";
import { AddExtension, githubSource } from "./AddExtension";
import { ExtensionDetails, githubUrl } from "./ExtensionParts";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const found: ExtensionStatus = {
  source: "github:a/b@v1", id: "tides", name: "Tides", version: "1.0.0", access: ["network: api.example.com"], provides: ["data"], running: false,
  settings: [
    { key: "apiKey", label: "API key", type: "secret", required: true, help: "From example.com", set: false },
    { key: "limit", label: "Limit", type: "number", required: false, default: 5, set: true },
  ],
};

afterEach(cleanup);
beforeEach(() => vi.restoreAllMocks());

const choose = async (item: RegExp) => {
  render(<AddExtension what="plugin" onAdded={onAdded} />);
  fireEvent.keyDown(screen.getByRole("button", { name: "Add plugin" }), { key: "Enter" });
  fireEvent.click(await screen.findByRole("menuitem", { name: item }));
};
const onAdded = vi.fn();
const github = async (repo: string, version: string, folder = "") => {
  await choose(/GitHub repo/);
  fireEvent.change(screen.getByLabelText("Repository"), { target: { value: repo } });
  fireEvent.change(screen.getByLabelText("Folder (optional)"), { target: { value: folder } });
  fireEvent.change(screen.getByLabelText("Version"), { target: { value: version } });
  fireEvent.click(screen.getByRole("button", { name: "Check" }));
};

describe("AddExtension", () => {
  it("offers all three places, with the marketplace as a link", async () => {
    await choose(/local files/);
    expect(screen.getByRole("dialog", { name: "Add a plugin from local files" })).toBeTruthy();
    fireEvent.keyDown(document.activeElement!, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    fireEvent.keyDown(screen.getByRole("button", { name: "Add plugin" }), { key: "Enter" });
    expect((await screen.findByRole("menuitem", { name: /marketplace/ })).getAttribute("href")).toBe("/marketplace");
    expect(screen.getByRole("menuitem", { name: /GitHub repo/ })).toBeTruthy();
  });

  it("shows what an extension can contact and needs before adding it", async () => {
    const inspect = vi.spyOn(api, "inspectExtension").mockResolvedValue(found);
    const add = vi.spyOn(api, "addExtension").mockResolvedValue(found);
    onAdded.mockClear();
    await github(" a/b ", " v1 ");
    await screen.findByText("network: api.example.com");
    expect(inspect).toHaveBeenCalledWith("github:a/b@v1");
    expect(screen.getByText("It needs this access:")).toBeTruthy();
    expect(add).not.toHaveBeenCalled();

    const key = screen.getByLabelText("API key") as HTMLInputElement;
    expect(key.type).toBe("password");
    fireEvent.change(key, { target: { value: "k" } });
    fireEvent.click(screen.getByRole("button", { name: "Allow this access and add Tides" }));
    await waitFor(() => expect(add).toHaveBeenCalledWith("github:a/b@v1", { limit: 5, apiKey: "k" }, found.access));
    await waitFor(() => expect(onAdded).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("needs a version before checking a GitHub repo", async () => {
    await choose(/GitHub repo/);
    fireEvent.change(screen.getByLabelText("Repository"), { target: { value: "a/b" } });
    expect((screen.getByRole("button", { name: "Check" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("checks a folder in the config folder", async () => {
    const inspect = vi.spyOn(api, "inspectExtension").mockResolvedValue(found);
    await choose(/local files/);
    fireEvent.change(screen.getByLabelText("Or use a folder in your config folder"), { target: { value: " ./extensions/tides " } });
    fireEvent.click(screen.getByRole("button", { name: "Check" }));
    await screen.findByText("It needs this access:");
    expect(inspect).toHaveBeenCalledWith("./extensions/tides");
  });

  it("shows why a source can't be used, and can go back from the review", async () => {
    const inspect = vi.spyOn(api, "inspectExtension").mockRejectedValueOnce(new Error("no extension.ts in this folder")).mockResolvedValue(found);
    await github("a/b", "v1");
    await screen.findByText("no extension.ts in this folder");
    fireEvent.click(screen.getByRole("button", { name: "Check" }));
    fireEvent.click(await screen.findByRole("button", { name: "Back" }));
    expect((screen.getByLabelText("Repository") as HTMLInputElement).value).toBe("a/b");
    expect(inspect).toHaveBeenCalledTimes(2);
  });
});

describe("githubSource", () => {
  it("builds a source from the fields, or a pasted address", () => {
    expect(githubSource("a/b", "", "v1")).toBe("github:a/b@v1");
    expect(githubSource("a/b", "/packages/tides/", "main")).toBe("github:a/b/packages/tides@main");
    expect(githubSource("https://github.com/a/b.git", "", "v1")).toBe("github:a/b@v1");
    expect(githubSource("https://github.com/a/b/", "", "v1")).toBe("github:a/b@v1");
    expect(githubSource("github:a/b", "", "v1")).toBe("github:a/b@v1");
  });
});

describe("AddExtension upload", () => {
  const file = new File(["x"], "tides.zip");
  const pick = async () => {
    await choose(/local files/);
    fireEvent.change(screen.getByLabelText("File for the plugin"), { target: { files: [file] } });
  };

  it("uploads a file and goes to the second step", async () => {
    const upload = vi.spyOn(api, "uploadExtension").mockResolvedValue({ ...found, source: "upload:abc", warnings: ["It can see your whole network"] });
    const add = vi.spyOn(api, "addExtension").mockResolvedValue(found);
    await pick();
    await screen.findByText("It needs this access:");
    expect(upload).toHaveBeenCalledWith(file);
    expect(screen.getAllByText(/tides\.zip/).length).toBeGreaterThan(0);
    expect(screen.getByText("It can see your whole network")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("API key"), { target: { value: "k" } });
    fireEvent.click(screen.getByRole("button", { name: "Allow this access and add Tides" }));
    await waitFor(() => expect(add).toHaveBeenCalledWith("upload:abc", { limit: 5, apiKey: "k" }, found.access));
  });

  it("shows an upload error and lets the same file be chosen again", async () => {
    vi.spyOn(api, "uploadExtension").mockRejectedValue(new Error("there is no extension.ts in this file"));
    await pick();
    await screen.findByText("there is no extension.ts in this file");
    expect((screen.getByLabelText("File for the plugin") as HTMLInputElement).value).toBe("");
  });

  it("says when an extension needs no access", async () => {
    vi.spyOn(api, "uploadExtension").mockResolvedValue({ ...found, access: [] });
    await pick();
    await screen.findByText("No access to anything outside itself");
  });
});

describe("ExtensionDetails", () => {
  it("asks for approval, calls approve, and shows blocked attempts", async () => {
    const approve = vi.spyOn(api, "approveExtension").mockResolvedValue({ approved: true });
    const onApproved = vi.fn();
    const blocked = { ...found, source: "upload:abc", needsApproval: true, warnings: ["Reads your whole home"], violations: [{ time: "2026-11-01T10:00:00Z", extension: "tides", kind: "network", detail: "evil.example.com" }] };
    render(<WithRole><ExtensionDetails extension={blocked} onRemoved={vi.fn()} onApproved={onApproved} /></WithRole>);
    expect(screen.getByText(/^Uploaded file/)).toBeTruthy();
    expect(screen.getByText("Blocked 1 attempt")).toBeTruthy();
    expect(screen.getByText("evil.example.com")).toBeTruthy();
    expect(screen.getByText("Reads your whole home")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Approve access" }));
    expect(approve).not.toHaveBeenCalled(); // the dialog explains what is being agreed to first
    fireEvent.click(await screen.findByRole("button", { name: "Approve and start" }));
    await waitFor(() => expect(approve).toHaveBeenCalledWith("upload:abc", found.access));
    await waitFor(() => expect(onApproved).toHaveBeenCalled());
  });

  it("offers no approval when none is needed", () => {
    render(<WithRole><ExtensionDetails extension={{ ...found, access: [] }} onRemoved={vi.fn()} /></WithRole>);
    expect(screen.queryByRole("button", { name: "Approve access" })).toBeNull();
    expect(screen.getByText("No access to anything outside itself")).toBeTruthy();
  });

  it("says a required setting is missing, and removing it says its saved settings go too", async () => {
    const remove = vi.spyOn(api, "removeExtension").mockResolvedValue({});
    const onRemoved = vi.fn();
    render(<WithRole><ExtensionDetails extension={found} onRemoved={onRemoved} /></WithRole>);
    expect(screen.getByText(/^Missing: /)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(await screen.findByText(/its saved settings \(such as keys\) are deleted/)).toBeTruthy();
    fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(remove).toHaveBeenCalledWith(found.source));
    expect(onRemoved).toHaveBeenCalled();
  });

  it("repeats the risk in the dialog that agrees to it", async () => {
    render(<WithRole><ExtensionDetails extension={{ ...found, needsApproval: true, risks: ["It can send your key to evil.example.com."] }} onRemoved={vi.fn()} /></WithRole>);
    fireEvent.click(screen.getByRole("button", { name: "Approve access" }));
    expect(within(await screen.findByRole("alertdialog")).getByText(/evil\.example\.com/)).toBeTruthy();
  });

  it("states the risk of approving before anything else", () => {
    render(<WithRole><ExtensionDetails extension={{ ...found, needsApproval: true, risks: ["It holds your key and can also send data to evil.example.com."] }} onRemoved={vi.fn()} /></WithRole>);
    expect(screen.getByText("The risk in approving this")).toBeTruthy();
    expect(screen.getByText(/evil\.example\.com/)).toBeTruthy();
  });

  it("folds the risk under its title once it's running, and opens it on a press", () => {
    render(<WithRole><ExtensionDetails extension={{ ...found, running: true, risks: ["It holds your key and can also send data to evil.example.com."], warnings: ["Reads a file"] }} onRemoved={vi.fn()} /></WithRole>);
    const title = screen.getByRole("button", { name: "The risk in approving this" });
    expect(title.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText(/evil\.example\.com/)).toBeNull();
    fireEvent.click(title);
    expect(title.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByText(/evil\.example\.com/)).toBeTruthy();
    expect(screen.queryByText("Reads a file")).toBeNull(); // each one opens on its own
  });

  it("leaves out approve and remove for people who can't manage extensions, but still shows the request", () => {
    const pending = { ...found, needsApproval: true };
    render(<WithRole role="editor"><ExtensionDetails extension={pending} onRemoved={vi.fn()} /></WithRole>);
    expect(screen.getByText("Waiting for your OK")).toBeTruthy();
    expect(screen.getByText("Only an administrator can approve it.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Approve access" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove" })).toBeNull();
  });
});

describe("ExtensionDetails changes", () => {
  const mine: ExtensionStatus = {
    ...found, source: "upload:abc", provides: ["data", "webhooks"], access: ["Receive messages from outside"],
    settings: [
      { key: "apiKey", label: "API key", type: "secret", required: true, set: true },
      { key: "city", label: "City", type: "string", required: false, set: true, value: "Warsaw" },
      { key: "metric", label: "Metric", type: "boolean", required: false, set: true, value: true },
    ],
  };

  it("edits settings without showing a secret, and keeps one that is left blank", async () => {
    const update = vi.spyOn(api, "updateExtensionSettings").mockResolvedValue(mine);
    const onChanged = vi.fn();
    render(<WithRole><ExtensionDetails extension={mine} onRemoved={vi.fn()} onChanged={onChanged} /></WithRole>);
    fireEvent.click(screen.getByRole("button", { name: "Edit settings" }));
    const key = (await screen.findByLabelText("API key")) as HTMLInputElement;
    expect(key.value).toBe("");
    expect(key.placeholder).toBe("Leave blank to keep it");
    const city = screen.getByLabelText("City (optional)") as HTMLInputElement;
    expect(city.value).toBe("Warsaw");
    fireEvent.change(city, { target: { value: "Gdansk" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith("upload:abc", { city: "Gdansk", metric: true }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it("keeps a file that isn't chosen again, and sends the text of one that is, never showing either", async () => {
    const withFile: ExtensionStatus = { ...mine, settings: [...mine.settings, { key: "devices", label: "Devices file", type: "file", required: true, set: true }] };
    const update = vi.spyOn(api, "updateExtensionSettings").mockResolvedValue(withFile);
    render(<WithRole><ExtensionDetails extension={withFile} onRemoved={vi.fn()} /></WithRole>);
    fireEvent.click(screen.getByRole("button", { name: "Edit settings" }));
    const picker = (await screen.findByLabelText("Devices file")) as HTMLInputElement;
    expect(picker.type).toBe("file");
    expect(picker.required).toBe(false);
    expect(screen.getByText("Set. Choose a file only to replace it.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith("upload:abc", { city: "Warsaw", metric: true }));

    fireEvent.click(screen.getByRole("button", { name: "Edit settings" }));
    const big = new File(["x".repeat((1 << 20) + 1)], "big.json");
    fireEvent.change(await screen.findByLabelText("Devices file"), { target: { files: [big] } });
    expect(await screen.findByText("big.json is over 1 MiB.")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Devices file"), { target: { files: [new File(['[{"key":"k"}]'], "tuya.json")] } });
    expect(await screen.findByText("tuya.json is read and will be kept encrypted.")).toBeTruthy();
    expect(screen.queryByText('[{"key":"k"}]')).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(update).toHaveBeenLastCalledWith("upload:abc", { city: "Warsaw", metric: true, devices: '[{"key":"k"}]' }));
  });

  it("sends a new secret, and clears a blank setting", async () => {
    const update = vi.spyOn(api, "updateExtensionSettings").mockResolvedValue(mine);
    render(<WithRole><ExtensionDetails extension={mine} onRemoved={vi.fn()} /></WithRole>);
    fireEvent.click(screen.getByRole("button", { name: "Edit settings" }));
    fireEvent.change(await screen.findByLabelText("API key"), { target: { value: "new-key" } });
    fireEvent.change(screen.getByLabelText("City (optional)"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(update).toHaveBeenCalledWith("upload:abc", { apiKey: "new-key", city: "", metric: true }));
  });

  it("shows why saving failed and stays open", async () => {
    vi.spyOn(api, "updateExtensionSettings").mockRejectedValue(new Error('the setting "city" must be a string'));
    render(<WithRole><ExtensionDetails extension={mine} onRemoved={vi.fn()} /></WithRole>);
    fireEvent.click(screen.getByRole("button", { name: "Edit settings" }));
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    await screen.findByText('the setting "city" must be a string');
    expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
  });

  it("offers editing only to someone who can manage", () => {
    const { rerender } = render(<WithRole><ExtensionDetails extension={mine} onRemoved={vi.fn()} /></WithRole>);
    for (const name of ["Edit settings", "Upload new version", "New webhook address"]) expect(screen.getByRole("button", { name })).toBeTruthy();
    rerender(<WithRole role="editor"><ExtensionDetails extension={mine} onRemoved={vi.fn()} /></WithRole>);
    for (const name of ["Edit settings", "Upload new version", "New webhook address"]) expect(screen.queryByRole("button", { name })).toBeNull();
  });

  it("shows the address a new webhook gets once, and the old one stops", async () => {
    const make = vi.spyOn(api, "newWebhookAddress").mockResolvedValue({ webhook: "/hooks/tides/new-token" });
    render(<WithRole><ExtensionDetails extension={mine} onRemoved={vi.fn()} /></WithRole>);
    fireEvent.click(screen.getByRole("button", { name: "New webhook address" }));
    expect(make).not.toHaveBeenCalled(); // says the old one stops first
    fireEvent.click(await screen.findByRole("button", { name: "Make a new address" }));
    const shown = await screen.findByTestId("webhook-address");
    expect(shown.textContent).toBe(`${window.location.origin}/hooks/tides/new-token`);
    fireEvent.click(screen.getByRole("button", { name: "I've saved it" }));
    await waitFor(() => expect(screen.queryByTestId("webhook-address")).toBeNull());
  });

  it("shows the webhook address that approving made", async () => {
    vi.spyOn(api, "approveExtension").mockResolvedValue({ approved: true, webhook: "/hooks/tides/first" });
    render(<WithRole><ExtensionDetails extension={{ ...mine, needsApproval: true }} onRemoved={vi.fn()} /></WithRole>);
    expect(screen.queryByRole("button", { name: "New webhook address" })).toBeNull(); // nothing to replace yet
    fireEvent.click(screen.getByRole("button", { name: "Approve access" }));
    fireEvent.click(await screen.findByRole("button", { name: "Approve and start" }));
    expect((await screen.findByTestId("webhook-address")).textContent).toContain("/hooks/tides/first");
  });

  it("replaces with an uploaded version after saying what it asks for", async () => {
    const next: ExtensionStatus = { ...found, source: "upload:def", version: "2.0.0", access: ["Receive messages from outside", "Saved data"] };
    const upload = vi.spyOn(api, "uploadExtension").mockResolvedValue(next);
    const replace = vi.spyOn(api, "replaceExtension").mockResolvedValue(next);
    const onChanged = vi.fn();
    render(<WithRole><ExtensionDetails extension={mine} onRemoved={vi.fn()} onChanged={onChanged} /></WithRole>);
    fireEvent.change(screen.getByLabelText("File with the new version"), { target: { files: [new File(["x"], "tides.zip")] } });
    await waitFor(() => expect(upload).toHaveBeenCalled());
    await screen.findByText(/asks for access the old one didn't have/);
    expect(screen.getByText("Saved data")).toBeTruthy();
    expect(replace).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Replace" }));
    await waitFor(() => expect(replace).toHaveBeenCalledWith("upload:abc", "upload:def", undefined, next.access));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it("leaves it as it was when the new version is refused", async () => {
    vi.spyOn(api, "uploadExtension").mockResolvedValue({ ...found, source: "upload:def", id: "other" });
    vi.spyOn(api, "replaceExtension").mockRejectedValue(new Error('that is "other", not "tides"'));
    const onChanged = vi.fn();
    render(<WithRole><ExtensionDetails extension={mine} onRemoved={vi.fn()} onChanged={onChanged} /></WithRole>);
    fireEvent.change(screen.getByLabelText("File with the new version"), { target: { files: [new File(["x"], "other.zip")] } });
    fireEvent.click(await screen.findByRole("button", { name: "Replace" }));
    expect((await screen.findByRole("alert")).textContent).toBe('that is "other", not "tides"');
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("asks what an upload needs before replacing", async () => {
    const pat = { key: "pat", label: "PAT", type: "secret" as const, required: true, set: false };
    const next: ExtensionStatus = { ...mine, source: "upload:def", version: "2.0.0", upgrade: { from: "1.0.0", to: "2.0.0", steps: [{ version: "2.0.0", note: "Needs a PAT." }], ask: [pat] } };
    vi.spyOn(api, "uploadExtension").mockResolvedValue(next);
    const replace = vi.spyOn(api, "replaceExtension").mockResolvedValue(next);
    render(<WithRole><ExtensionDetails extension={mine} onRemoved={vi.fn()} /></WithRole>);
    fireEvent.change(screen.getByLabelText("File with the new version"), { target: { files: [new File(["x"], "tides.zip")] } });
    await screen.findByText("Needs a PAT.", { exact: false });
    const update = screen.getByRole("button", { name: "Replace" }) as HTMLButtonElement;
    expect(update.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("PAT"), { target: { value: "x" } });
    fireEvent.click(update);
    await waitFor(() => expect(replace).toHaveBeenCalledWith("upload:abc", "upload:def", { pat: "x" }, next.access));
  });

  it("updates from GitHub through the same steps", async () => {
    const pat = { key: "pat", label: "PAT", type: "secret" as const, required: true, set: false };
    const here = { ...mine, source: "github:a/b@v1" };
    const next: ExtensionStatus = { ...mine, source: "github:a/b@v2", version: "2.0.0", upgrade: { from: "1.0.0", to: "2.0.0", steps: [], ask: [pat] } };
    const inspect = vi.spyOn(api, "inspectExtension").mockResolvedValue(next);
    const replace = vi.spyOn(api, "replaceExtension").mockResolvedValue(next);
    render(<WithRole><ExtensionDetails extension={here} onRemoved={vi.fn()} /></WithRole>);
    fireEvent.click(screen.getByRole("button", { name: "Update from GitHub" }));
    const check = await screen.findByRole("button", { name: "Check" }) as HTMLButtonElement;
    expect(check.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("New version"), { target: { value: "github:a/b@v2" } });
    fireEvent.click(check);
    fireEvent.change(await screen.findByLabelText("PAT"), { target: { value: "x" } });
    expect(inspect).toHaveBeenCalledWith("github:a/b@v2");
    fireEvent.click(screen.getByRole("button", { name: "Replace" }));
    await waitFor(() => expect(replace).toHaveBeenCalledWith("github:a/b@v1", "github:a/b@v2", { pat: "x" }, next.access));
  });
});

describe("AddExtension webhooks", () => {
  it("shows the webhook address that adding made", async () => {
    vi.spyOn(api, "inspectExtension").mockResolvedValue({ ...found, settings: [] });
    vi.spyOn(api, "addExtension").mockResolvedValue({ ...found, webhook: "/hooks/tides/abc" });
    await github("a/b", "v1");
    fireEvent.click(await screen.findByRole("button", { name: "Allow this access and add Tides" }));
    expect((await screen.findByTestId("webhook-address")).textContent).toBe(`${window.location.origin}/hooks/tides/abc`);
  });
});

describe("what the marketplace took back", () => {
  it("says so on the card, keeps it running, and stops it only when asked", async () => {
    const stop = vi.spyOn(api, "stopExtension").mockResolvedValue({});
    const revoked: ExtensionStatus = { ...found, running: true, source: "market:tides@1.0.0#" + "a".repeat(64), revoked: { id: "tides", version: "1.0.0", state: "withdrawn", reason: "It leaks your key" } };
    render(<WithRole><ExtensionDetails extension={revoked} onRemoved={vi.fn()} /></WithRole>);
    expect(screen.getByText("The marketplace withdrew this version")).toBeTruthy();
    expect(screen.getByText(/It leaks your key/)).toBeTruthy();
    expect(screen.getByText(/could leave something like your heating unmanaged/)).toBeTruthy();
    expect(stop).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Stop it" }));
    fireEvent.click(await screen.findByRole("button", { name: "Stop it" }));
    await waitFor(() => expect(stop).toHaveBeenCalledWith(revoked.source));
  });
});

describe("what comes straight from GitHub", () => {
  it("is labelled as not verified, before adding and after", async () => {
    render(<WithRole><ExtensionDetails extension={{ ...found, source: "github:a/b@v1" }} onRemoved={vi.fn()} /></WithRole>);
    expect(screen.getByText("Not verified")).toBeTruthy();
    cleanup();
    render(<WithRole><ExtensionDetails extension={{ ...found, source: "market:tides@1.0.0#" + "a".repeat(64) }} onRemoved={vi.fn()} /></WithRole>);
    expect(screen.queryByText("Not verified")).toBeNull();
  });
});

describe("ExtensionDetails, from the marketplace", () => {
  const added = (source: string): ExtensionStatus => ({ ...found, source, running: true, settings: [] });

  it("links back to the listing", () => {
    window.history.pushState(null, "", "/plugins");
    render(<WithRole role="viewer"><ExtensionDetails extension={added("market:tides@1.0.0#" + "a".repeat(64))} onRemoved={() => {}} /></WithRole>);
    const link = screen.getByRole("link", { name: "Marketplace" });
    expect(link.getAttribute("href")).toBe("/marketplace/tides");
    fireEvent.click(link);
    expect(window.location.pathname).toBe("/marketplace/tides");
  });

  it("shows the version and links a GitHub source to its folder at that tag", () => {
    render(<WithRole role="viewer"><ExtensionDetails extension={{ ...added("github:drax98/tomtly-extensions/tuya@tuya-v1.2.4"), version: "1.2.4" }} onRemoved={() => {}} /></WithRole>);
    expect(screen.getByText("1.2.4")).toBeTruthy();
    expect(screen.getByRole("link", { name: "GitHub" }).getAttribute("href")).toBe("https://github.com/drax98/tomtly-extensions/tree/tuya-v1.2.4/tuya");
  });

  it("makes GitHub links only from github: sources", () => {
    expect(githubUrl("github:a/b@v1")).toBe("https://github.com/a/b/tree/v1");
    expect(githubUrl("github:a/b/x/y@main")).toBe("https://github.com/a/b/tree/main/x/y");
    for (const source of ["upload:abc", "market:x@1#aa", "github:a@v1", "https://github.com/a/b"]) expect(githubUrl(source)).toBeUndefined();
  });

  it("has no such link for anything else", () => {
    for (const source of ["github:a/b@v1", "upload:abc", "local/market:x@1"]) {
      render(<WithRole role="viewer"><ExtensionDetails extension={added(source)} onRemoved={() => {}} /></WithRole>);
      expect(screen.queryByRole("link", { name: "Marketplace" })).toBeNull();
      cleanup();
    }
  });
});

describe("ExtensionDetails marketplace update", () => {
  const installed: ExtensionStatus = {
    ...found, source: "market:tides@1.0.0#" + "a".repeat(64),
    marketUpdate: { version: "1.2.0", steps: [{ version: "1.1.0", note: "New units." }, { version: "1.2.0", note: "It needs a token now.", ask: ["token"] }] },
  };

  it("says a newer version is available, what each step changes, and updates through the marketplace", async () => {
    const next: ExtensionStatus = { ...installed, source: "market:tides@1.2.0#" + "b".repeat(64), version: "1.2.0", marketUpdate: undefined, upgrade: { from: "1.0.0", to: "1.2.0", steps: [], ask: [] } } as ExtensionStatus;
    const inspect = vi.spyOn(api, "inspectMarketEntry").mockResolvedValue(next);
    render(<WithRole><ExtensionDetails extension={installed} onRemoved={vi.fn()} /></WithRole>);
    expect(screen.getByText("Version 1.2.0 is available")).toBeTruthy();
    const steps = screen.getByRole("list", { name: "What changes" });
    expect(steps.textContent).toContain("1.1.0: New units.");
    expect(steps.textContent).toContain("1.2.0: It needs a token now. It will ask you for 1 setting.");
    fireEvent.click(screen.getByRole("button", { name: "Update to 1.2.0" }));
    await waitFor(() => expect(inspect).toHaveBeenCalledWith("tides"));
    expect(await screen.findByRole("dialog", { name: "Update Tides to 1.2.0?" })).toBeTruthy();
  });

  it("only tells a viewer, and says nothing when there is no update", () => {
    const { unmount } = render(<WithRole role="viewer"><ExtensionDetails extension={installed} onRemoved={vi.fn()} /></WithRole>);
    expect(screen.getByText("Version 1.2.0 is available")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Update to 1.2.0" })).toBeNull();
    unmount();
    render(<WithRole><ExtensionDetails extension={{ ...installed, marketUpdate: undefined }} onRemoved={vi.fn()} /></WithRole>);
    expect(screen.queryByText(/is available/)).toBeNull();
  });
});
