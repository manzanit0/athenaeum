import { expect, test } from "@playwright/test";
import fs from "node:fs";
import { addBook, megaDelay, megaSignIn, notice } from "./helpers";

test.afterEach(() => fs.rmSync(megaDelay, { force: true }));

test("previews how much a sync transfers and shows its progress", async ({ page, request }) => {
  await megaSignIn(request);
  for (const file of ["kindred.epub", "piranesi.epub", "stoner.epub", "beloved.epub", "left-hand.epub"]) {
    await addBook(request, file);
  }
  await page.goto("/sync");
  await expect(page.getByText(/Signed in to MEGA as/)).toBeVisible();
  const change = page.getByRole("button", { name: "Change" });
  if (await change.isVisible()) {
    await change.click();
  }
  await page.getByLabel("MEGA folder").fill("Books/Progress");
  await page.getByRole("button", { name: "Save folder" }).click();
  await expect(notice(page, "MEGA folder saved")).toBeVisible();

  await page.getByRole("button", { name: "More options" }).click();
  await page.getByRole("button", { name: "Upload to MEGA" }).click();
  await expect(notice(page, "Preview ready")).toBeVisible();
  const preview = page.getByRole("group", { name: "Sync preview" });
  await expect(preview).toContainText(/\d+ books: \d+ to add, [\d.]+ (KB|MB) to upload/);

  fs.writeFileSync(megaDelay, "150ms");
  await preview.getByRole("button", { name: "Make these changes" }).click();
  const progress = page.getByRole("group", { name: "Sync progress" });
  await expect(progress.getByRole("progressbar", { name: "Uploading" })).toBeVisible();
  await expect(progress).toContainText(/Uploading \d+ of \d+, [\d.]+ (B|KB|MB) of [\d.]+ (KB|MB)/);
  await expect(page.getByRole("button", { name: "Upload to MEGA" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Sync", exact: true })).toBeDisabled();
  const early = Number(await progress.getByRole("progressbar").getAttribute("aria-valuenow"));
  await expect
    .poll(async () => Number(await progress.getByRole("progressbar").getAttribute("aria-valuenow").catch(() => 100)))
    .toBeGreaterThan(early);

  await expect(notice(page, "Sync finished")).toBeVisible({ timeout: 30_000 });
  await expect(progress).toHaveCount(0);
  await expect(page.getByRole("group", { name: "Last sync" })).toContainText(/\d+ books: \d+ added, [\d.]+ (KB|MB) uploaded/);
  await expect(page.getByRole("button", { name: "Upload to MEGA" })).toBeEnabled();
});
