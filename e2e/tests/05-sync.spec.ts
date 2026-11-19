import { expect, type Locator, type Page, test } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import { addBook, fixture, mega, megaDrive, megaSignIn, notice, shelfBook } from "./helpers";

const folder = path.join(megaDrive, "Books", "Library");

/** Finds, inside a sync report, the changes to one side or the books that share a fate. */
function part(within: Locator, name: string): Locator {
  return within.getByRole("group", { name, exact: true });
}

/** Opens the whole list of a group of changes when it is cut short. */
async function showAll(group: Locator): Promise<void> {
  const more = group.getByRole("button", { name: /^Show all/ });
  if ((await more.count()) > 0) {
    await more.click();
  }
}

/** Finds the button that syncs both ways. */
function syncButton(page: Page): Locator {
  return page.getByRole("button", { name: "Sync", exact: true });
}

/** Waits for the preview a sync starts with and makes its changes. */
async function confirm(page: Page): Promise<void> {
  await expect(notice(page, "Preview ready")).toBeVisible();
  await page.getByRole("group", { name: "Sync preview" }).getByRole("button", { name: "Make these changes" }).click();
  await expect(notice(page, "Sync finished")).toBeVisible();
}

/** Uploads the library to MEGA, through the options and the preview. */
async function upload(page: Page): Promise<void> {
  await page.getByRole("button", { name: "More options" }).click();
  await page.getByRole("button", { name: "Upload to MEGA" }).click();
  await confirm(page);
}

test("signs in to MEGA with a password and a two-factor code, and out again", async ({ page, request }) => {
  await request.delete("/api/sync/session");
  await page.goto("/sync");
  await expect(syncButton(page)).toBeDisabled();
  await expect(page.getByText("Sign in to MEGA above to turn this on.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Upload to MEGA" })).toHaveCount(0);
  await page.getByRole("button", { name: "More options" }).click();
  await expect(page.getByRole("button", { name: "Upload to MEGA" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Download from MEGA" })).toBeDisabled();
  await expect(page.getByLabel("Also delete what the other side no longer has")).not.toBeChecked();
  const signIn = page.getByRole("button", { name: "Sign in", exact: true });

  await page.getByLabel("MEGA email").fill(mega.email);
  await page.getByLabel("MEGA password").fill("not the password");
  await signIn.click();
  await expect(page.getByRole("alert").filter({ hasText: "MEGA did not accept the sign-in" })).toBeVisible();
  await expect(page.getByLabel("Two-factor code")).toHaveCount(0);

  await page.getByLabel("MEGA email").fill(mega.guardedEmail);
  await page.getByLabel("MEGA password").fill(mega.password);
  await signIn.click();
  await expect(page.getByRole("alert").filter({ hasText: "Enter the code from your authenticator app." })).toBeVisible();
  await page.getByLabel("Two-factor code").fill("24 68 10");
  await expect(page.getByLabel("Two-factor code")).toHaveValue(mega.code);
  await signIn.click();
  await expect(notice(page, "Signed in to MEGA")).toBeVisible();
  await expect(page.getByText(`Signed in to MEGA as ${mega.guardedEmail}.`)).toBeVisible();
  await expect(page.getByLabel("MEGA password")).toHaveCount(0);

  await page.reload();
  await expect(page.getByText(`Signed in to MEGA as ${mega.guardedEmail}.`)).toBeVisible();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(notice(page, "Signed out of MEGA")).toBeVisible();
  await expect(page.getByLabel("MEGA password")).toHaveValue("");

  await page.getByLabel("MEGA email").fill(mega.email);
  await page.getByLabel("MEGA password").fill(mega.password);
  await signIn.click();
  await expect(page.getByText(`Signed in to MEGA as ${mega.email}.`)).toBeVisible();
});

test("syncs the library with a MEGA folder in both directions", async ({ page, request }) => {
  await megaSignIn(request);
  const local = await addBook(request, "middlemarch.epub");
  const uuid = "e2e00001-0000-4000-8000-000000000001";
  const file = "Italo Calvino/Invisible Cities - Italo Calvino.epub";
  fs.mkdirSync(path.join(folder, "Italo Calvino"), { recursive: true });
  fs.mkdirSync(path.join(folder, "_athenaeum"), { recursive: true });
  fs.copyFileSync(fixture("ficciones.epub"), path.join(folder, file));
  fs.writeFileSync(
    path.join(folder, "_athenaeum", `${uuid}.json`),
    JSON.stringify({
      uuid,
      title: "Invisible Cities",
      authors: ["Italo Calvino"],
      tags: ["Fiction"],
      created_at: new Date().toISOString(),
      files: [file],
    }),
  );

  await page.goto("/sync");
  await expect(page.getByText("Signed in to MEGA as reader@example.com")).toBeVisible();
  await page.getByLabel("MEGA folder").fill("Books/Library");
  await page.getByRole("button", { name: "Save folder" }).click();
  await expect(notice(page, "MEGA folder saved")).toBeVisible();
  await expect(page.getByText("Signed in to MEGA as reader@example.com, syncing with the folder Books/Library.")).toBeVisible();
  await expect(page.getByLabel("MEGA folder")).toHaveCount(0);
  await page.getByRole("button", { name: "Change" }).click();
  await expect(page.getByLabel("MEGA folder")).toHaveValue("Books/Library");
  await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
  await page.getByRole("button", { name: "Done" }).click();
  await expect(page.getByLabel("MEGA folder")).toHaveCount(0);

  const preview = page.getByRole("group", { name: "Sync preview" });
  await expect(page.getByLabel("Preview only, change nothing")).toHaveCount(0);
  await syncButton(page).click();
  await expect(notice(page, "Preview ready")).toBeVisible();
  await expect(preview).toContainText("Nothing has changed yet.");
  const uploads = part(preview, "On MEGA");
  await expect(uploads).toContainText(/\d+ books: \d+ to add, [\d.]+ (KB|MB) to upload/);
  await showAll(part(uploads, "Add"));
  await expect(part(uploads, "Add")).toContainText("Middlemarch by George Eliot");
  await expect(part(uploads, "Remove")).toHaveCount(0);
  const downloads = part(preview, "In this library");
  await expect(downloads).toContainText(/1 book: 1 to add, [\d.]+ (B|KB) to download/);
  await expect(part(downloads, "Add")).toContainText("Invisible Cities by Italo Calvino");
  expect(fs.existsSync(path.join(folder, local.path))).toBe(false);

  await preview.getByRole("button", { name: "Make these changes" }).click();
  await expect(notice(page, "Sync finished")).toBeVisible();
  const lastSync = page.getByRole("group", { name: "Last sync" });
  await expect(part(lastSync, "On MEGA")).toContainText(/\d+ books: \d+ added/);
  await expect(part(lastSync, "In this library")).toContainText("1 book: 1 added");
  await expect(part(part(lastSync, "In this library"), "Added").getByRole("link", { name: "Invisible Cities" })).toBeVisible();
  expect(fs.existsSync(path.join(folder, local.path, local.formats[0].filename))).toBe(true);
  expect(fs.existsSync(path.join(folder, "_athenaeum", `${local.uuid}.json`))).toBe(true);

  await page.goto("/");
  await expect(shelfBook(page, "Invisible Cities", "Italo Calvino")).toBeVisible();
});

test("removes a deleted book from MEGA on the next sync", async ({ page, request }) => {
  await megaSignIn(request);
  const book = await addBook(request, "ficciones.epub");
  const uploaded = path.join(folder, book.path, book.formats[0].filename);
  await page.goto("/sync");
  await upload(page);
  expect(fs.existsSync(uploaded)).toBe(true);

  await page.goto(`/books/${book.id}/edit`);
  await page.getByRole("button", { name: "Delete book" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Delete book" }).click();
  await expect(notice(page, "Deleted “Ficciones”")).toBeVisible();

  await page.goto("/sync");
  await expect(page.getByText(/\d+ files deleted or renamed here will leave MEGA with the next sync/)).toBeVisible();
  await syncButton(page).click();
  await expect(notice(page, "Preview ready")).toBeVisible();
  const preview = page.getByRole("group", { name: "Sync preview" });
  await expect(part(preview, "On MEGA")).toContainText("1 book: 1 to remove");
  await expect(part(preview, "Remove")).toContainText("Ficciones by Jorge Luis Borges");
  await expect(part(preview, "Remove")).toContainText("goes to its Rubbish Bin");
  await expect(part(preview, "In this library")).toContainText("nothing to change");
  expect(fs.existsSync(uploaded)).toBe(true);

  await preview.getByRole("button", { name: "Make these changes" }).click();
  await expect(notice(page, "Sync finished")).toBeVisible();
  const lastSync = page.getByRole("group", { name: "Last sync" });
  await expect(part(lastSync, "On MEGA")).toContainText("1 book: 1 removed");
  await expect(part(lastSync, "Removed")).toContainText("Ficciones by Jorge Luis Borges");
  expect(fs.existsSync(uploaded)).toBe(false);
  expect(fs.existsSync(path.join(folder, "_athenaeum", `${book.uuid}.json`))).toBe(false);

  await page.goto("/");
  await expect(shelfBook(page, "Ficciones", "Jorge Luis Borges")).toHaveCount(0);
});

test("files a book put on MEGA by hand under its author", async ({ page, request }) => {
  await megaSignIn(request);
  const loose = path.join(folder, "To read", "some download.epub");
  fs.mkdirSync(path.dirname(loose), { recursive: true });
  fs.copyFileSync(fixture("cosmicomics.epub"), loose);

  await page.goto("/sync");
  await syncButton(page).click();
  await confirm(page);
  const filed = part(page.getByRole("group", { name: "Last sync" }), "In this library");
  await expect(filed).toContainText("1 book: 1 added");
  await expect(part(filed, "Added")).toContainText("Cosmicomics by Italo Calvino");

  await page.goto("/");
  await expect(shelfBook(page, "Cosmicomics", "Italo Calvino")).toBeVisible();

  await page.goto("/sync");
  await syncButton(page).click();
  await confirm(page);
  expect(fs.existsSync(loose)).toBe(false);
  expect(fs.existsSync(path.join(folder, "Italo Calvino", "Cosmicomics - Italo Calvino.epub"))).toBe(true);
});

test("shows a retitled book as one update, not as a deletion and an upload", async ({ page, request }) => {
  await megaSignIn(request);
  const book = await addBook(request, "harbour.epub");
  const before = path.join(folder, book.path, book.formats[0].filename);
  await page.goto("/sync");
  await upload(page);
  expect(fs.existsSync(before)).toBe(true);

  await page.goto(`/books/${book.id}/edit`);
  await page.getByLabel("Title").fill("Harbour Lights at Dusk");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(notice(page, "Changes saved")).toBeVisible();

  await page.goto("/sync");
  await syncButton(page).click();
  await expect(notice(page, "Preview ready")).toBeVisible();
  const preview = page.getByRole("group", { name: "Sync preview" });
  await expect(part(preview, "On MEGA")).toContainText("1 book: 1 to update");
  const update = part(preview, "Update");
  await expect(update).toContainText("Harbour Lights at Dusk by Nell Ward");
  await expect(update).toContainText("Details changed, EPUB renamed");
  await expect(update).toContainText(`${book.path}/${book.formats[0].filename} is now`);
  await expect(part(preview, "Add")).toHaveCount(0);
  await expect(part(preview, "Remove")).toHaveCount(0);
  await expect(part(preview, "In this library")).toContainText("nothing to change");

  await preview.getByRole("button", { name: "Make these changes" }).click();
  await expect(notice(page, "Sync finished")).toBeVisible();
  const lastSync = page.getByRole("group", { name: "Last sync" });
  await expect(part(lastSync, "On MEGA")).toContainText("1 book: 1 updated");
  await expect(part(lastSync, "Updated").getByRole("link", { name: "Harbour Lights at Dusk" })).toBeVisible();
  expect(fs.existsSync(before)).toBe(false);
  expect(fs.existsSync(path.join(folder, book.path, "Harbour Lights at Dusk - Nell Ward.epub"))).toBe(true);

  await syncButton(page).click();
  await expect(preview).toContainText("Nothing to do: the library and MEGA already match.");
  await expect(preview.getByRole("button", { name: "Make these changes" })).toHaveCount(0);
});
