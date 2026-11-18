import { createContext, useContext } from "react";
import type { SystemInfo } from "./api";

/** The capabilities assumed until the server has answered. */
export const fallbackSystem: SystemInfo = {
  version: "",
  import_formats: [],
  output_formats: [],
  device_formats: [],
  can_convert: false,
  native_conversions: {},
  can_embed: false,
};

/** Carries the server capabilities to every page. */
export const SystemContext = createContext<SystemInfo>(fallbackSystem);

/** Returns what the running server can do. */
export function useSystem(): SystemInfo {
  return useContext(SystemContext);
}
