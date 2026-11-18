import { type APIRequestContext, expect, type Locator, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";

/** The folder the test server keeps its data, fake Kindle and fake MEGA in. */
export const work = path.join(__dirname, "..", ".work");

/** The folder standing in for the Kindle's documents folder. */
export const kindleDocuments = path.join(work, "kindle", "documents");

/** The folder standing in for the MEGA Cloud Drive. */
export const megaDrive = path.join(work, "mega");

/** The file whose content, such as "150ms", slows every change of the fake mega-cli down. */
export const megaDelay = path.join(work, "mega-delay");

/** The accounts the stand-in for MEGA knows, as defined in internal/megasync/megatest. */
export const mega = {
  email: "reader@example.com",
  password: "correct horse battery staple",
  guardedEmail: "guarded@example.com",
  code: "246810",
};

/** Signs in to the stand-in for MEGA through the API. */
export async function megaSignIn(request: APIRequestContext): Promise<void> {
  const response = await request.post("/api/sync/session", {
    data: { email: mega.email, password: mega.password },
  });
  expect(response.status()).toBe(200);
}

/** A library book as returned by the API. */
export interface Book {
  id: number;
  uuid: string;
  title: string;
  path: string;
  formats: { format: string; filename: string }[];
}

/** Returns the path of a sample file generated for the tests. */
export function fixture(name: string): string {
  return path.join(work, "fixtures", name);
}

/** Adds a sample file to the library through the API. */
export async function addBook(request: APIRequestContext, file: string): Promise<Book> {
  const response = await request.post("/api/books", {
    multipart: { files: fs.createReadStream(fixture(file)) },
  });
  expect(response.status()).toBe(201);
  return (await response.json()).books[0];
}

/** Finds the notice confirming that something succeeded. */
export function notice(page: Page, text: string | RegExp): Locator {
  return page.getByRole("status").filter({ hasText: text });
}

/** Finds a book on the library shelf by its title and author. */
export function shelfBook(page: Page, title: string, author: string): Locator {
  return page.getByRole("link", { name: `${title} by ${author}`, exact: true });
}
