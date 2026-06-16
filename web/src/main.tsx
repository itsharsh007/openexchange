import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import "./styles.css";

// Standard React 18 entry point. StrictMode double-invokes effects in dev to
// surface side-effect bugs — our WS hook is written to tolerate this (it cleans
// up the socket and timers on unmount, so the dev double-mount won't leak).
ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
