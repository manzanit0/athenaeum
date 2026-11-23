import { createContext, useContext } from "react";
import type { Book, SystemInfo } from "./api";

/** The capabilities assumed until the server has answered. */
export const fallbackSystem: SystemInfo = {
  version: "",
  import_formats: [],
  output_formats: [],
  device_formats: [],
  can_convert: false,
  native_conversions: {},
  can_embed: false,
  kept_formats: [],
};

/**
 * Reports whether a book has a file the application can open: one to
 * read, convert or send to the Kindle. The formats that are only kept
 * do not count.
 */
export function usable(book: Book, system: SystemInfo): boolean {
  return book.formats.some((file) => !system.kept_formats.includes(file.format));
}

/** Carries the server capabilities to every page. */
export const SystemContext = createContext<SystemInfo>(fallbackSystem);

/** Returns what the running server can do. */
export function useSystem(): SystemInfo {
  return useContext(SystemContext);
}
