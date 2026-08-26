import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Testing Library keeps rendered trees in the document until told otherwise, so
// a component from one test would still be findable in the next.
afterEach(cleanup);
