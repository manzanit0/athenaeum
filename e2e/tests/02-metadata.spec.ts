import { expect, test } from "@playwright/test";
import { addBook, fixture, notice, shelfBook } from "./helpers";

test("edits the details and cover of a book", async ({ page, request }) => {
  const book = await addBook(request, "kindred.epub");
  await page.goto(`/books/${book.id}`);
  await expect(page.getByRole("heading", { level: 1, name: "Kindred" })).toBeVisible();

  await page.getByLabel("Title").fill("Kindred: A Novel");
  await page.getByLabel("Authors").fill("Octavia E. Butler, Damian Duffy");
  await page.getByLabel("Series", { exact: true }).fill("Graphic Adaptations");
  await page.getByLabel("Number in series").fill("3");
  await page.getByLabel("Publisher").fill("Beacon Press");
  const categories = page.getByRole("group", { name: "Categories" });
  await categories.getByLabel("New category").fill("Fiction/time travel ");
  await categories.getByRole("button", { name: "Add" }).click();
  await categories.getByLabel("New category").fill("Classics");
  await categories.getByLabel("New category").press("Enter");
  await categories.getByLabel("New category").fill("Fiction");
  await categories.getByRole("button", { name: "Add" }).click();
  await expect(categories.getByRole("listitem")).toContainText(["Classics", "Fiction"]);
  await categories.getByRole("button", { name: "Remove Fiction" }).click();
  await categories.getByLabel("New category").fill("Fiction / Time Travel");
  await categories.getByRole("button", { name: "Add" }).click();
  await page.getByLabel("Description").fill("Dana is pulled back in time to a Maryland plantation.");
  const published = page.getByRole("group", { name: "Published" });
  await published.getByLabel("Year").fill("the 19x7");
  await expect(published.getByLabel("Year")).toHaveValue("197");
  await expect(published.getByLabel("Day")).toBeDisabled();
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(published.getByRole("alert")).toHaveText("Write the year with four digits.");
  await published.getByLabel("Year").fill("1979");
  await published.getByLabel("Month").selectOption({ label: "June" });
  await published.getByLabel("Day").fill("31");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(published.getByRole("alert")).toHaveText("The publication date must be a real date.");
  await published.getByLabel("Day").fill("9");
  await page.getByRole("button", { name: "Save changes" }).click();

  await expect(notice(page, "Changes saved")).toBeVisible();
  await expect(page.getByRole("heading", { level: 1, name: "Kindred: A Novel" })).toBeVisible();

  await page.reload();
  await expect(page.getByLabel("Authors")).toHaveValue("Octavia E. Butler, Damian Duffy");
  await expect(page.getByLabel("Number in series")).toHaveValue("3");
  await expect(published.getByLabel("Year")).toHaveValue("1979");
  await expect(published.getByLabel("Month")).toHaveValue("6");
  await expect(published.getByLabel("Day")).toHaveValue("9");
  await expect(categories.getByRole("listitem")).toContainText(["Classics", "Fiction / Time Travel"]);
  await expect(page.getByLabel("Description")).toHaveValue("Dana is pulled back in time to a Maryland plantation.");

  await page.getByLabel("Change cover").setInputFiles(fixture("cover.png"));
  await expect(notice(page, "Cover changed")).toBeVisible();
  await page.getByRole("button", { name: "Remove cover" }).click();
  await expect(notice(page, "Cover removed")).toBeVisible();

  await page.goto("/");
  await page.getByLabel("Search library").fill("time travel");
  await expect(shelfBook(page, "Kindred: A Novel", "Octavia E. Butler, Damian Duffy")).toBeVisible();
});
