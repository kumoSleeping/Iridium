// The entry of a public link's read-only view (view.html): its styles, then the view itself
// (viewpage.tsx), apart so the libraries it draws with are not counted in the panel's first screen.
import "./styles.css";

void import("./viewpage").then((m) => m.mount());
