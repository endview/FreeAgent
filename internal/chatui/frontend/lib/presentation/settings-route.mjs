// Keep saved management links and menu navigation inside the settings shell.
export function settingsDestination(view = "chat", section = "providers") {
  const aliases = {
    manage: "space-content",
    trash: "trash",
    retained: "retained",
  };
  return Object.hasOwn(aliases, view)
    ? { view: "settings", section: aliases[view] }
    : { view, section };
}

export function isSpaceSettings(section) {
  return ["space-content", "spaces", "trash", "retained"].includes(section);
}
