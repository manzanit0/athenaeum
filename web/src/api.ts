import { listQuery, type View } from "./shelf";

/** One file of a book. */
export interface Format {
  format: string;
  filename: string;
  size: number;
  created_at: string;
}

/** The editable description of a book. */
export interface Metadata {
  title: string;
  authors: string[];
  series: string;
  series_index: number;
  publisher: string;
  language: string;
  isbn: string;
  description: string;
  tags: string[];
  published: string;
}

/** A library entry. */
export interface Book extends Metadata {
  id: number;
  uuid: string;
  path: string;
  cover: string;
  formats: Format[];
  created_at: string;
  updated_at: string;
}

/** What to search the internet for when looking for the cover of a book. */
export interface CoverQuery {
  title: string;
  author: string;
  isbn: string;
}

/** A cover image found on the internet, which can be chosen for a book. */
export interface CoverCandidate {
  id: string;
  /** The website the image came from. */
  source: string;
  width: number;
  height: number;
}

/** A website that a search for covers could not ask, and why. */
export interface CoverFailure {
  source: string;
  reason: string;
}

/** What a search for covers found, best first. */
export interface CoverSearch {
  candidates: CoverCandidate[];
  /** The websites that could not be asked. */
  failed: CoverFailure[];
}

/** How many books share a value. */
export interface Tally {
  value: string;
  count: number;
}

/** How the books on the shelf spread over what the shelf can be narrowed by. */
export interface Facets {
  total: number;
  matching: number;
  tags: Tally[];
  untagged: number;
  languages: Tally[];
  formats: Tally[];
}

/** How far a running sync has come. */
export interface SyncProgress {
  phase: "preparing" | "uploading" | "downloading" | "finishing";
  done: number;
  total: number;
  bytes_done: number;
  bytes_total: number;
  current: string;
}

/** A background task such as a conversion or a sync. */
export interface Job {
  id: number;
  kind: string;
  title: string;
  status: "running" | "done" | "failed";
  error: string;
  result?: unknown;
  progress?: SyncProgress;
  book_id?: number;
  created_at: string;
  finished_at?: string;
}

/** How far the restore of a book has come. */
export interface RestoreProgress {
  stage: "sample" | "pages" | "headings" | "writing";
  pages: number;
  read: number;
  cost: number;
}

/** A passage a restore could not read with certainty. */
export interface Doubt {
  page: number;
  text: string;
  reason: string;
}

/** What the restore of a book did. */
export interface RestoreReport {
  pages: number;
  scans: number;
  sections: number;
  headings: number;
  headings_changed: number;
  notes_linked: number;
  doubts: Doubt[];
  warnings: string[];
  cost: number;
}

/** What restoring a book would involve. */
export interface RestoreOutlook {
  configured: boolean;
  model: string;
  possible: boolean;
  reason?: string;
  pages: number;
  scans: number;
  text: number;
  sample: number;
  cost_low: number;
  cost_high: number;
  limit: number;
  replaces: boolean;
}

/** Returns how far a running restore has come, when it has said. */
export function restoreProgressOf(job: Job): RestoreProgress | undefined {
  const progress = job.progress as Partial<RestoreProgress> | undefined;
  return job.kind === "restore" && progress?.stage ? (progress as RestoreProgress) : undefined;
}

/** Returns what a finished restore did. */
export function restoreReportOf(job: Job): RestoreReport | undefined {
  const report = job.result as Partial<RestoreReport> | undefined;
  return job.kind === "restore" && job.status === "done" && Array.isArray(report?.doubts)
    ? (report as RestoreReport)
    : undefined;
}

/** What the running server can do. */
export interface SystemInfo {
  version: string;
  import_formats: string[];
  output_formats: string[];
  device_formats: string[];
  can_convert: boolean;
  native_conversions: Record<string, string[]>;
  can_embed: boolean;
  kept_formats: string[];
}

/** Returns what a finished job says it had to leave out. */
export function warningsOf(job: Job): string[] {
  const warnings = (job.result as { warnings?: unknown } | undefined)?.warnings;
  return Array.isArray(warnings) ? warnings.filter((entry): entry is string => typeof entry === "string") : [];
}

/** User editable configuration. */
export interface Settings {
  mega_folder: string;
  kindle_path: string;
}

/** The state of the connected Kindle. */
export interface DeviceStatus {
  connected: boolean;
  path: string;
  total_bytes: number;
  free_bytes: number;
  formats: string[];
}

/** A book stored on the Kindle. */
export interface DeviceBook {
  path: string;
  name: string;
  format: string;
  size: number;
  modified: string;
  title: string;
  authors: string[];
  book_id?: number;
}

/** One change made or planned by a sync. */
export interface SyncAction {
  op: "mkdir" | "copy" | "update" | "delete";
  path: string;
  size?: number;
}

/** The outcome of one sync direction. */
export interface SyncResult {
  actions: SyncAction[];
  dry_run: boolean;
  files: number;
  bytes: number;
  folders: number;
  deleted: number;
}

/** What a sync does to one file. */
export interface FileChange {
  op: "add" | "replace" | "remove" | "rename";
  path: string;
  from?: string;
  size?: number;
}

/** What a sync does to one book of a side. */
export interface BookChange {
  kind: "add" | "update" | "remove";
  book_id?: number;
  title: string;
  authors: string[];
  details: boolean;
  cover?: "add" | "replace" | "remove";
  files: FileChange[];
  bytes: number;
}

/** What a sync changes on one side, book by book. */
export interface SyncSummary {
  books: BookChange[];
  other: FileChange[];
}

/** What a sync run did. */
export interface SyncReport {
  direction: SyncDirection;
  delete: boolean;
  dry_run: boolean;
  folder: string;
  removed: string[];
  push?: SyncResult;
  pull?: SyncResult;
  rescan?: { added: number; updated: number; removed: number };
  mega?: SyncSummary;
  library?: SyncSummary;
  /** The files a sync both ways found changed on both sides. */
  conflicts?: SyncConflict[];
}

/** A file changed on both sides since they last matched. */
export interface SyncConflict {
  path: string;
  /** The side whose version stays. */
  kept: "library" | "mega";
}

/** The direction of a sync run. */
export type SyncDirection = "push" | "pull" | "both";

/** How an attempt to sign in to MEGA went. */
export interface SignIn {
  signed_in: boolean;
  error: string;
  code_required: boolean;
}

/** The state of MEGA sync. */
export interface SyncInfo {
  signed_in: boolean;
  account: string;
  error: string;
  folder: string;
  pending_deletions: number;
  last_run?: Job;
}

/** One document of the reading order of a book. */
export interface PreviewSection {
  href: string;
  title: string;
}

/** One line of the table of contents of a book. */
export interface PreviewEntry {
  title: string;
  href: string;
  level: number;
}

/** How to show a book: where its content is and how it is divided. */
export interface Preview {
  kind: "epub" | "pdf" | "text";
  format: string;
  converted: boolean;
  base: string;
  sections: PreviewSection[];
  contents: PreviewEntry[];
}

/** Reports whether the answer to a preview request is a job to wait for. */
export function isJob(answer: Preview | Job): answer is Job {
  return "status" in answer;
}

/** The outcome of uploading files to the library. */
export interface ImportResult {
  books: Book[];
  errors: { file: string; error: string }[];
}

/** A library the application knows: a folder and whether it is open. */
export interface LibraryEntry {
  path: string;
  name: string;
  state: "open" | "closed" | "missing";
}

/** Why no library is open. */
export interface LibraryProblem {
  path: string;
  missing: boolean;
  detail: string;
}

/** The known libraries and which one is open; current is empty for none. */
export interface Libraries {
  home: string;
  current: string;
  problem?: LibraryProblem;
  libraries: LibraryEntry[];
}

/** What the server found at a typed folder path. */
export interface FolderCheck {
  path: string;
  kind: "library" | "empty" | "other" | "new" | "invalid";
  detail?: string;
}

/** An answer in which the server refuses a request, with its reason as a code when it gives one. */
export class Refusal extends Error {
  code: string;

  /** Keeps the code beside the message. */
  constructor(message: string, code = "") {
    super(message);
    this.code = code;
  }
}

/** Sends a request and returns the decoded JSON response. */
async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method };
  if (body instanceof FormData || body instanceof Blob) {
    init.body = body;
  } else if (body !== undefined) {
    init.body = JSON.stringify(body);
    init.headers = { "Content-Type": "application/json" };
  }
  const response = await fetch(path, init);
  const text = await response.text();
  const data = text ? JSON.parse(text) : undefined;
  if (!response.ok && !(response.status === 422 && data?.errors)) {
    throw new Refusal(data?.error ?? `The server answered ${response.status}.`, data?.code);
  }
  return data as T;
}

/** Builds a multipart form holding the given files. */
function filesForm(files: File[]): FormData {
  const form = new FormData();
  for (const file of files) {
    form.append("files", file, file.name);
  }
  return form;
}

/** Every call the interface makes, one function per API endpoint. */
export const api = {
  libraries: () => request<Libraries>("GET", "/api/libraries"),
  openLibrary: (path: string, create: boolean, force: boolean) =>
    request<Libraries>("POST", "/api/libraries", { path, create, force }),
  forgetLibrary: (path: string) => request<Libraries>("DELETE", `/api/libraries?${new URLSearchParams({ path })}`),
  checkFolder: (path: string) => request<FolderCheck>("GET", `/api/libraries/check?${new URLSearchParams({ path })}`),
  suggestFolders: (path: string) => request<string[]>("GET", `/api/libraries/folders?${new URLSearchParams({ path })}`),

  system: () => request<SystemInfo>("GET", "/api/system"),
  settings: () => request<Settings>("GET", "/api/settings"),
  saveSettings: (settings: Partial<Settings>) => request<Settings>("PUT", "/api/settings", settings),

  books: (view: View) => request<Book[]>("GET", `/api/books?${listQuery(view)}`),
  facets: (view: View) => request<Facets>("GET", `/api/books/facets?${listQuery(view)}`),
  tagBooks: (ids: number[], add: string[], remove: string[]) =>
    request<Book[]>("POST", "/api/books/tags", { ids, add, remove }),
  renameTag: (from: string, to: string) => request<{ books: number }>("POST", "/api/books/tags/rename", { from, to }),
  book: (id: number) => request<Book>("GET", `/api/books/${id}`),
  importBooks: (files: File[]) => request<ImportResult>("POST", "/api/books", filesForm(files)),
  updateBook: (id: number, metadata: Metadata) => request<Book>("PATCH", `/api/books/${id}`, metadata),
  deleteBook: (id: number) => request<void>("DELETE", `/api/books/${id}`),
  setCover: (id: number, image: Blob) => request<Book>("PUT", `/api/books/${id}/cover`, image),
  removeCover: (id: number) => request<Book>("DELETE", `/api/books/${id}/cover`),
  findCovers: (id: number, query: CoverQuery) =>
    request<CoverSearch>("GET", `/api/books/${id}/covers?${new URLSearchParams({ ...query })}`),
  chooseFoundCover: (id: number, candidate: string) =>
    request<Book>("POST", `/api/books/${id}/cover/found`, { id: candidate }),
  addFormat: (id: number, files: File[]) => request<Book>("POST", `/api/books/${id}/formats`, filesForm(files)),
  deleteFormat: (id: number, format: string) => request<Book>("DELETE", `/api/books/${id}/formats/${format}`),
  convert: (id: number, to: string) => request<Job>("POST", `/api/books/${id}/convert`, { to }),
  restoreOutlook: (id: number) => request<RestoreOutlook>("GET", `/api/books/${id}/restore`),
  restore: (id: number, replace: boolean) => request<Job>("POST", `/api/books/${id}/restore`, { replace }),
  saveModelKey: (key: string) => request<void>("PUT", "/api/restore/key", { key }),
  preview: (id: number, format: string) =>
    request<Preview | Job>("GET", `/api/books/${id}/preview?${new URLSearchParams(format ? { format } : {})}`),

  jobs: () => request<Job[]>("GET", "/api/jobs"),
  job: (id: number) => request<Job>("GET", `/api/jobs/${id}`),

  device: () => request<DeviceStatus>("GET", "/api/device"),
  deviceBooks: () => request<DeviceBook[]>("GET", "/api/device/books"),
  sendToDevice: (bookId: number) => request<Job>("POST", "/api/device/books", { book_id: bookId }),
  deleteFromDevice: (path: string) =>
    request<void>("DELETE", `/api/device/books?${new URLSearchParams({ path })}`),
  importFromDevice: (path: string) => request<Book>("POST", "/api/device/import", { path }),

  sync: () => request<SyncInfo>("GET", "/api/sync"),
  signIn: async (email: string, password: string, code: string): Promise<SignIn> => {
    const response = await fetch("/api/sync/session", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email, password, code }),
    });
    const data = await response.json().catch(() => ({}));
    return {
      signed_in: response.ok,
      error: response.ok ? "" : (data.error ?? `The server answered ${response.status}.`),
      code_required: Boolean(data.code_required),
    };
  },
  signOut: () => request<{ confirmed: boolean }>("DELETE", "/api/sync/session"),
  startSync: (direction: SyncDirection, remove: boolean, dryRun: boolean) =>
    request<Job>("POST", "/api/sync", { direction, delete: remove, dry_run: dryRun }),
};

/** Returns the address of a book's cover image. */
export function coverUrl(book: Book): string {
  return `/api/books/${book.id}/cover?v=${encodeURIComponent(book.updated_at)}`;
}

/** Returns the address of the image of a cover that a search found. */
export function foundCoverUrl(candidate: CoverCandidate): string {
  return `/api/covers/${candidate.id}`;
}

/** Returns the address that downloads one file of a book. */
export function downloadUrl(book: Book, format: string): string {
  return `/api/books/${book.id}/formats/${format}`;
}
