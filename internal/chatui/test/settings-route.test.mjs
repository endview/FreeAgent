import test from "node:test";
import assert from "node:assert/strict";
import { settingsDestination, isSpaceSettings } from "../frontend/lib/presentation/settings-route.mjs";

test("saved management links enter the settings shell without losing their destination", () => {
  assert.deepEqual(settingsDestination("manage"), { view: "settings", section: "space-content" });
  assert.deepEqual(settingsDestination("trash"), { view: "settings", section: "trash" });
  assert.deepEqual(settingsDestination("retained"), { view: "settings", section: "retained" });
  assert.deepEqual(settingsDestination("settings", "spaces"), { view: "settings", section: "spaces" });
  assert.deepEqual(settingsDestination("chat", "memory"), { view: "chat", section: "memory" });
  assert.deepEqual(settingsDestination("toString"), { view: "toString", section: "providers" });
  for (const section of ["space-content", "spaces", "trash", "retained"])
    assert.equal(isSpaceSettings(section), true);
  assert.equal(isSpaceSettings("providers"), false);
});
