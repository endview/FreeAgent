import type { PointerEvent as ReactPointerEvent } from "react";
export function Divider({
  axis,
  label,
  value,
  min,
  max,
  onChange,
  reset,
}: {
  axis: "x" | "y";
  label: string;
  value: number;
  min: number;
  max: number;
  onChange: (value: number) => void;
  reset: number;
}) {
  function start(event: ReactPointerEvent<HTMLDivElement>) {
    event.preventDefault();
    const element = event.currentTarget;
    element.setPointerCapture(event.pointerId);
    const initial = axis === "x" ? event.clientX : event.clientY;
    const move = (next: PointerEvent) =>
      onChange(
        Math.max(
          min,
          Math.min(
            max,
            value +
              (axis === "x" ? next.clientX - initial : initial - next.clientY),
          ),
        ),
      );
    const end = () => {
      element.removeEventListener("pointermove", move);
      element.removeEventListener("pointerup", end);
      element.removeEventListener("pointercancel", end);
    };
    element.addEventListener("pointermove", move);
    element.addEventListener("pointerup", end);
    element.addEventListener("pointercancel", end);
  }
  return (
    <div
      className={`divider divider-${axis}`}
      role="separator"
      tabIndex={0}
      aria-label={label}
      aria-orientation={axis === "x" ? "vertical" : "horizontal"}
      aria-valuenow={value}
      aria-valuemin={min}
      aria-valuemax={max}
      onPointerDown={start}
      onDoubleClick={() => onChange(reset)}
      onKeyDown={(event) => {
        const change = ["ArrowRight", "ArrowUp"].includes(event.key)
          ? 16
          : ["ArrowLeft", "ArrowDown"].includes(event.key)
            ? -16
            : 0;
        if (change) {
          event.preventDefault();
          onChange(Math.max(min, Math.min(max, value + change)));
        }
        if (event.key === "Home") {
          event.preventDefault();
          onChange(reset);
        }
      }}
    >
      <span />
    </div>
  );
}
