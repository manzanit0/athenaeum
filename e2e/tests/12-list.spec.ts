import { expect, test } from "@playwright/test";
import { addBook } from "./helpers";

test("shows the shelf as a list and sorts it by its columns", async ({ page, request }) => {
  const details = [
    { file: "harbour.epub", published: "2011-03", publisher: "Quay Press" },
    { file: "anchors.epub", published: "1987", publisher: "Tidewrack" },
    { file: "mooring.epub", published: "", publisher: "" },
  ];
  for (const { file, published, publisher } of details) {
    const book = await addBook(request, file);
    const data = { tags: ["Nautical"], published, publisher };
    expect((await request.patch(`/api/books/${book.id}`, { data })).ok()).toBe(true);
  }
  const titles = () => page.getByRole("table").locator("tbody tr a");

  await page.goto("/?tag=Nautical");
  await expect(page.getByRole("table")).toHaveCount(0);
  const layout = page.getByRole("group", { name: "Show books as" });
  await layout.getByRole("button", { name: "List" }).click();
  await expect(titles()).toHaveText(["Anchors Aweigh", "Harbour Lights", "Mooring Lines"]);
  await expect(page.getByRole("columnheader", { name: "Title" })).toHaveAttribute("aria-sort", "ascending");
  await expect(page.getByRole("row", { name: /Harbour Lights/ })).toContainText("Quay Press");
  await expect(page.getByRole("row", { name: /Harbour Lights/ })).toContainText("Nautical");

  const menu = page.getByRole("menu", { name: "Sort" });
  const field = (name: string) => menu.getByRole("group", { name: "Sort by" }).getByRole("menuitemradio", { name });
  const order = (name: string) => menu.getByRole("group", { name: "Order" }).getByRole("menuitemradio", { name });
  const open = () => page.getByRole("button", { name: "Sort", exact: true }).click();
  await open();
  await expect(field("Title")).toBeChecked();
  await expect(order("A to Z")).toBeChecked();
  await expect(order("Z to A")).not.toBeChecked();
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
  await expect(page.getByRole("row", { name: /Harbour Lights/ })).toContainText("Mar 2011");

  await page.getByRole("columnheader", { name: "Title" }).getByRole("button").click();
  await expect(titles()).toHaveText(["Mooring Lines", "Harbour Lights", "Anchors Aweigh"]);
  await expect(page.getByRole("columnheader", { name: "Title" })).toHaveAttribute("aria-sort", "descending");
  await open();
  await expect(order("Z to A")).toBeChecked();
  await page.keyboard.press("Escape");

  await page.getByRole("columnheader", { name: "Published" }).getByRole("button").click();
  await expect(page).toHaveURL(/sort=published%3Adesc/);
  await expect(titles()).toHaveText(["Harbour Lights", "Anchors Aweigh", "Mooring Lines"]);
  await open();
  await expect(field("Published")).toBeChecked();
  await expect(order("Newest first")).toBeChecked();
  await order("Oldest first").click();
  await expect(menu).toHaveCount(0);
  await expect(titles()).toHaveText(["Anchors Aweigh", "Harbour Lights", "Mooring Lines"]);
  await expect(page.getByRole("columnheader", { name: "Published" })).toHaveAttribute("aria-sort", "ascending");

  await open();
  await field("Author").click();
  await expect(titles()).toHaveText(["Anchors Aweigh", "Mooring Lines", "Harbour Lights"]);
  await expect(page.getByRole("columnheader", { name: "Author" })).toHaveAttribute("aria-sort", "ascending");
  await expect(page.getByRole("columnheader", { name: "Published" })).toHaveAttribute("aria-sort", "none");
  await open();
  await expect(order("A to Z")).toBeChecked();
  await page.keyboard.press("Escape");
  await expect(page.getByLabel("Select Anchors Aweigh")).toBeVisible();

  await page.reload();
  await expect(titles()).toHaveText(["Anchors Aweigh", "Mooring Lines", "Harbour Lights"]);
  await titles().first().click();
  await expect(page.getByRole("heading", { level: 1, name: "Anchors Aweigh" })).toBeVisible();
  await page.getByRole("link", { name: "Back to the library" }).click();
  await expect(titles()).toHaveCount(3);

  await layout.getByRole("button", { name: "Grid" }).click();
  await expect(page.getByRole("table")).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Anchors Aweigh by Abel Finch", exact: true })).toBeVisible();
});
