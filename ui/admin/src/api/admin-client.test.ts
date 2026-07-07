import { describe, expect, it } from "vitest";
import { opportunitiesAdminAuthScopes } from "./admin-client";

describe("opportunities admin auth runtime scopes", () => {
  it("requests only scopes allowed by the OAuth client", () => {
    expect(opportunitiesAdminAuthScopes).toEqual([
      "openid",
      "profile",
      "offline_access",
    ]);
  });
});
