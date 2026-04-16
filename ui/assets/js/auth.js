// OIDC Authorization Code + PKCE flow for stawi.jobs
document.addEventListener("alpine:init", () => {
  const params = document.querySelector("meta[name=site-params]");
  const config = params ? JSON.parse(params.content) : {};

  window.oidcLogin = function () {
    return {
      loading: false,
      error: "",
      async startLogin() {
        this.loading = true;
        this.error = "";
        try {
          const codeVerifier = generateCodeVerifier();
          const codeChallenge = await generateCodeChallenge(codeVerifier);
          const state = generateState();

          sessionStorage.setItem("oidc_code_verifier", codeVerifier);
          sessionStorage.setItem("oidc_state", state);

          const authURL = new URL(
            config.oidcIssuer + "/protocol/openid-connect/auth"
          );
          authURL.searchParams.set("response_type", "code");
          authURL.searchParams.set("client_id", config.oidcClientID);
          authURL.searchParams.set("redirect_uri", config.oidcRedirectURI);
          authURL.searchParams.set("scope", "openid profile email");
          authURL.searchParams.set("code_challenge", codeChallenge);
          authURL.searchParams.set("code_challenge_method", "S256");
          authURL.searchParams.set("state", state);

          window.location.href = authURL.toString();
        } catch (e) {
          this.error = "Failed to start login. Please try again.";
          this.loading = false;
        }
      },
    };
  };

  window.oidcCallback = function () {
    return {
      loading: true,
      error: "",
      async init() {
        try {
          const urlParams = new URLSearchParams(window.location.search);
          const code = urlParams.get("code");
          const state = urlParams.get("state");
          const errorParam = urlParams.get("error");

          if (errorParam) {
            this.error = urlParams.get("error_description") || errorParam;
            this.loading = false;
            return;
          }

          if (!code || !state) {
            this.error = "Invalid callback parameters.";
            this.loading = false;
            return;
          }

          const savedState = sessionStorage.getItem("oidc_state");
          if (state !== savedState) {
            this.error = "State mismatch. Please try logging in again.";
            this.loading = false;
            return;
          }

          const codeVerifier = sessionStorage.getItem("oidc_code_verifier");
          if (!codeVerifier) {
            this.error = "Missing code verifier. Please try logging in again.";
            this.loading = false;
            return;
          }

          const tokenURL =
            config.oidcIssuer + "/protocol/openid-connect/token";
          const body = new URLSearchParams({
            grant_type: "authorization_code",
            client_id: config.oidcClientID,
            code: code,
            redirect_uri: config.oidcRedirectURI,
            code_verifier: codeVerifier,
          });

          const resp = await fetch(tokenURL, {
            method: "POST",
            headers: { "Content-Type": "application/x-www-form-urlencoded" },
            body: body.toString(),
          });

          if (!resp.ok) {
            this.error = "Token exchange failed. Please try again.";
            this.loading = false;
            return;
          }

          const tokens = await resp.json();

          sessionStorage.removeItem("oidc_code_verifier");
          sessionStorage.removeItem("oidc_state");

          Alpine.store("auth").setToken(tokens.access_token);
          await Alpine.store("auth").fetchProfile();

          const profile = Alpine.store("auth").profile;
          if (profile && profile.candidate) {
            window.location.href = "/dashboard/";
          } else {
            window.location.href = "/onboarding/";
          }
        } catch (e) {
          this.error = "Authentication failed. Please try again.";
          this.loading = false;
        }
      },
    };
  };
});

function generateCodeVerifier() {
  const array = new Uint8Array(32);
  crypto.getRandomValues(array);
  return base64URLEncode(array);
}

async function generateCodeChallenge(verifier) {
  const encoder = new TextEncoder();
  const data = encoder.encode(verifier);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return base64URLEncode(new Uint8Array(digest));
}

function generateState() {
  const array = new Uint8Array(16);
  crypto.getRandomValues(array);
  return base64URLEncode(array);
}

function base64URLEncode(buffer) {
  return btoa(String.fromCharCode(...buffer))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}
