import { expect, type Page, test } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import { addBook, notice, shelfBook, work } from "./helpers";

/** The folder the test server starts with, which is its first library. */
const home = path.join(work, "data");

/** The folder the tests keep their other libraries in. */
const shelves = path.join(work, "shelves");

/** Finds the control beside the application's name, which names the open library. */
function switcher(page: Page, name: string) {
  return page.getByRole("button", { name: `Library: ${name}`, exact: true });
}

test.beforeEach(() => {
  fs.rmSync(shelves, { recursive: true, force: true });
  fs.mkdirSync(shelves, { recursive: true });
});

test.afterEach(async ({ request }) => {
  const response = await request.post("/api/libraries", { data: { path: home, force: true } });
  expect(response.status()).toBe(200);
  for (const library of (await response.json()).libraries) {
    if (library.path !== home) {
      await request.delete("/api/libraries", { params: { path: library.path } });
    }
  }
});

test("adds a second library, switches between the two from the menu, and removes it from the list", async ({ page, request }) => {
  await addBook(request, "middlemarch.epub");
  const novels = path.join(shelves, "Novels");

  await page.goto("/");
  await switcher(page, "Default library").click();
  const menu = page.getByRole("menu", { name: "Libraries" });
  await expect(menu.getByRole("menuitemradio", { name: "Default library" })).toBeChecked();
  await menu.getByRole("menuitem", { name: "Add or remove libraries…" }).click();
  await expect(page.getByRole("heading", { name: "Libraries", exact: true })).toBeVisible();
  await page.getByLabel("Folder").fill(novels);
  await expect(page.getByText("This folder does not exist yet.")).toBeVisible();
  await page.getByRole("button", { name: "Create folder and start a library" }).click();

  await expect(switcher(page, "Novels")).toBeVisible();
  await expect(page).toHaveURL(/\/$/);
  await expect(shelfBook(page, "Middlemarch", "George Eliot")).toHaveCount(0);
  expect(fs.existsSync(path.join(novels, "athenaeum.db"))).toBe(true);

  await switcher(page, "Novels").click();
  await expect(menu.getByRole("menuitemradio", { name: "Novels" })).toBeChecked();
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
  await switcher(page, "Novels").click();
  await menu.getByRole("menuitemradio", { name: "Default library" }).click();
  await expect(switcher(page, "Default library")).toBeVisible();
  await expect(shelfBook(page, "Middlemarch", "George Eliot").first()).toBeVisible();

  await page.goto("/libraries");
  await page.getByRole("button", { name: "Remove Novels from the list" }).click();
  await expect(notice(page, "Removed “Novels” from the list")).toBeVisible();
  await expect(page.getByText(novels)).toHaveCount(0);
  expect(fs.existsSync(path.join(novels, "athenaeum.db"))).toBe(true);
  await switcher(page, "Default library").click();
  await expect(menu.getByRole("menuitemradio")).toHaveCount(1);
  await page.keyboard.press("Escape");

  await page.getByLabel("Folder").fill(novels);
  await expect(page.getByText("This folder holds a library.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Open library" })).toBeEnabled();
});

test("says what is at a typed path before anything is opened", async ({ page }) => {
  const loose = path.join(shelves, "Loose");
  fs.mkdirSync(loose);
  fs.writeFileSync(path.join(loose, "notes.txt"), "kept");

  await page.goto("/libraries");
  const folder = page.getByLabel("Folder");
  await folder.fill("shelves/Loose");
  await expect(page.getByText("Give the whole path of the folder, starting with /.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Open library" })).toBeDisabled();

  await folder.fill(path.join(loose, "notes.txt"));
  await expect(page.getByText("This is a file, not a folder.")).toBeVisible();

  await folder.fill(path.join(shelves, "Lo"));
  await expect(page.locator("datalist option")).toHaveAttribute("value", `${shelves}/Loose/`);

  await folder.fill(loose);
  await expect(page.getByText("This folder holds other files and no library.")).toBeVisible();
  await page.getByRole("button", { name: "Start a library beside these files" }).click();
  await expect(switcher(page, "Loose")).toBeVisible();
  expect(fs.readFileSync(path.join(loose, "notes.txt"), "utf8")).toBe("kept");
});

test("shows a library whose folder is gone as not found, without recreating it", async ({ page, request }) => {
  const gone = path.join(shelves, "Unplugged");
  expect((await request.post("/api/libraries", { data: { path: gone, create: true } })).status()).toBe(200);
  expect((await request.post("/api/libraries", { data: { path: home } })).status()).toBe(200);
  fs.rmSync(gone, { recursive: true });

  await page.goto("/libraries");
  const row = page.getByRole("listitem").filter({ hasText: "Unplugged" });
  await expect(row).toContainText("Folder not found");
  await expect(row.getByRole("button", { name: "Open Unplugged" })).toBeDisabled();
  await switcher(page, "Default library").click();
  await expect(page.getByRole("menuitemradio", { name: "Unplugged not found" })).toBeDisabled();
  expect(fs.existsSync(gone)).toBe(false);
});
