import { Component, lazy, Suspense, type ReactNode } from "react";
const Markdown = lazy(() => import("./Markdown"));
class MessageBoundary extends Component<
  { text: string; children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? (
      <pre className="stream-tail">{this.props.text}</pre>
    ) : (
      this.props.children
    );
  }
}
export function Message({
  text,
  streaming = false,
}: {
  text: string;
  streaming?: boolean;
}) {
  return (
    <MessageBoundary text={text}>
      <Suspense fallback={<div className="stream-tail">{text}</div>}>
        <Markdown text={text} streaming={streaming} />
      </Suspense>
    </MessageBoundary>
  );
}
