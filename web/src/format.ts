const units = ["B", "KB", "MB", "GB", "TB"];

/** Formats a byte count for people, such as "1.4 MB". */
export function bytes(value: number): string {
  let amount = value;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit++;
  }
  const digits = unit === 0 || amount >= 100 ? 0 : 1;
  return `${amount.toFixed(digits)} ${units[unit]}`;
}

/** Formats a timestamp as a short local date and time. */
export function dateTime(value: string): string {
  return new Date(value).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/**
 * Writes a publication date, kept as "2019", "2019-03" or "2019-03-12",
 * for people: the month by name, so that it is never taken for the day.
 */
export function publishedOn(value: string): string {
  const [year, month, day] = value.split("-").map(Number);
  if (!month) {
    return value;
  }
  const date = new Date(Date.UTC(year, month - 1, day || 1));
  return date.toLocaleDateString(undefined, {
    timeZone: "UTC",
    year: "numeric",
    month: "short",
    ...(day ? { day: "numeric" } : {}),
  });
}

/** Lists the names of the twelve months in the reader's language. */
export function monthNames(): string[] {
  return Array.from({ length: 12 }, (_, month) =>
    new Date(Date.UTC(2001, month, 1)).toLocaleDateString(undefined, { timeZone: "UTC", month: "long" }),
  );
}

/** Joins author names for display. */
export function byline(authors: string[]): string {
  return authors.length > 0 ? authors.join(", ") : "Unknown author";
}

/** Splits a comma separated field into trimmed, non-empty entries. */
export function splitList(value: string): string[] {
  return value
    .split(",")
    .map((entry) => entry.trim())
    .filter(Boolean);
}

/** Returns "1 book" or "3 books" for the given count and noun. */
export function count(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? "" : "s"}`;
}

/** Writes an amount of dollars, such as "$4.20". */
export function dollars(amount: number): string {
  return `$${amount.toFixed(2)}`;
}

/** Writes what the server gave as a reason as a sentence: capital first, full stop last. */
export function sentence(text: string): string {
  return text ? `${text.charAt(0).toUpperCase()}${text.slice(1)}.` : "";
}

/** Returns the message of a thrown value. */
export function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** Returns the name of a language code for people, such as "Spanish" for "es". */
export function languageName(code: string): string {
  try {
    const name = new Intl.DisplayNames(undefined, { type: "language" }).of(code) ?? code;
    return name === code ? code.toUpperCase() : name.charAt(0).toUpperCase() + name.slice(1);
  } catch {
    return code.toUpperCase();
  }
}
