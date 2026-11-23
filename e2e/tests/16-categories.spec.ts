import { expect, test } from "@playwright/test";
import { addBook, notice } from "./helpers";

test("clears the categories a book was imported with and files it afresh", async ({ page, request }) => {
  const book = await addBook(request, "packet.epub");
  await page.goto(`/books/${book.id}/edit`);
  const categories = page.getByRole("group", { name: "Categories" });
  const filed = categories.getByRole("listitem");
  const field = categories.getByLabel("Add a category");
  const choices = categories.getByRole("listbox");
  const said = categories.getByRole("status");
  await expect(filed).toHaveCount(12);
  await expect(categories).toContainText("This book has 12 categories, and a book takes up to three.");

  await categories.getByRole("button", { name: "Remove FIC000000" }).click();
  await expect(categories.getByRole("button", { name: "Remove General" })).toBeFocused();
  await expect(said).toHaveText("Removed “FIC000000”.");
  await categories.getByRole("button", { name: "Undo" }).click();
  await expect(filed).toHaveCount(12);

  await categories.getByRole("button", { name: "Remove all" }).click();
  await expect(filed).toHaveCount(0);
  await expect(said).toHaveText("Removed all 12 categories.");
  await expect(field).toBeFocused();
  await categories.getByRole("button", { name: "Undo" }).click();
  await expect(filed).toHaveCount(12);
  await categories.getByRole("button", { name: "Remove all" }).click();

  await field.click();
  await expect(choices.getByRole("option").first()).toContainText("just removed");
  await choices.getByRole("option", { name: "Sea Stories just removed" }).click();
  await expect(choices).toBeHidden();
  await field.fill("mari");
  await field.press("Enter");
  await field.fill("sea stories/whaling");
  await expect(choices.getByRole("option", { name: "Add “Sea Stories / whaling” new category" })).toBeVisible();
  await field.press("Enter");
  await expect(filed).toContainText(["Maritime", "Sea Stories / whaling"]);
  await field.press("ArrowDown");
  await field.press("ArrowDown");
  await field.press("ArrowUp");
  await field.press("Enter");
  await expect(filed).toContainText(["Maritime", "Sea Stories / whaling", "Fiction"]);

  await field.fill("lit");
  await expect(choices).toContainText("A book takes up to three categories. Remove one to add another.");
  await field.fill("maritime");
  await expect(choices).toContainText("This book is already filed under “maritime”.");
  await field.press("Escape");
  await expect(choices).toBeHidden();
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(notice(page, "Changes saved")).toBeVisible();
  await expect(page.getByRole("link", { name: "Books in Sea Stories / whaling" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Books in FIC000000" })).toHaveCount(0);
  await expect(page.getByRole("list", { name: "Filed under" }).getByRole("listitem")).toHaveCount(3);
});

test("keeps a book with a great many categories readable and quick to clear", async ({ page, request }) => {
  const book = await addBook(request, "handbook.pdf");
  const details = await (await request.get(`/api/books/${book.id}`)).json();
  const tags = Array.from({ length: 60 }, (_, index) => `Keyword ${String(index + 1).padStart(2, "0")}`);
  const filedAll = await request.patch(`/api/books/${book.id}`, { data: { ...details, tags } });
  expect(filedAll.status()).toBe(200);

  await page.goto(`/books/${book.id}`);
  const under = page.getByRole("list", { name: "Filed under" });
  await expect(under.getByRole("link")).toHaveCount(6);
  await expect(page.getByRole("link", { name: "Edit book" })).toBeInViewport();
  await under.getByRole("button", { name: "and 54 more" }).click();
  await expect(under.getByRole("link")).toHaveCount(60);

  await page.goto(`/books/${book.id}/edit`);
  const categories = page.getByRole("group", { name: "Categories" });
  const filed = categories.getByRole("listitem");
  const field = categories.getByLabel("Add a category");
  const choices = categories.getByRole("listbox");
  await expect(filed).toHaveCount(12);
  await categories.getByRole("button", { name: "Show all 60" }).click();
  await expect(filed).toHaveCount(60);
  await categories.getByRole("button", { name: "Show the first 12 only" }).click();
  await categories.getByRole("button", { name: "Remove Keyword 12" }).click();
  await expect(filed).toContainText(["Keyword 13"]);
  await expect(categories.getByRole("button", { name: "Show all 59" })).toBeVisible();

  await categories.getByRole("button", { name: "Remove all" }).click();
  await expect(categories.getByRole("status")).toHaveText("Removed all 59 categories.");
  await field.click();
  await expect(choices.getByRole("option").first()).not.toContainText("just removed");
  await expect(choices.getByRole("option")).toHaveCount(40);
  await expect(choices).toContainText("more. Type to narrow them down.");
  await field.fill("keyword 07");
  await field.press("Enter");
  await expect(filed).toContainText(["Keyword 07"]);

  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(notice(page, "Changes saved")).toBeVisible();
  await expect(under.getByRole("link")).toHaveCount(1);
});
