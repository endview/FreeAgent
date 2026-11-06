import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { installPlatform } from "@/lib/platform";
import "@/styles/tokens.css";
import { DesktopMenu } from "@/components/DesktopMenu";
installPlatform();
window.addEventListener("vite:preloadError", (event) => {
  event.preventDefault();
  window.dispatchEvent(new Event("freeagent-refresh-required"));
});
const root = createRoot(document.getElementById("app")!);
if (
  import.meta.env.MODE === "development" &&
  new URLSearchParams(location.search).get("view") === "gallery"
) {
  import("@/dev/Gallery").then(({ default: Gallery }) =>
    root.render(
      <StrictMode>
        <Gallery />
      </StrictMode>,
    ),
  );
} else {
  root.render(
    <StrictMode>
      <DesktopMenu>
        <App />
      </DesktopMenu>
    </StrictMode>,
  );
}
