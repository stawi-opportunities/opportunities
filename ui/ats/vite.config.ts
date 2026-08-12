import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "path";

export default defineConfig(({ command }) => ({
  plugins: [react()],
  // Production: Hugo copies ui/static/ats → public/ats, served at /ats/.
  base: command === "build" ? "/ats/" : "/",
  build: {
    outDir: resolve(__dirname, "../static/ats"),
    emptyOutDir: true,
  },
  server: {
    port: 5175,
    proxy: {
      // Connect procedures: /ats.v1.AtsService/*
      "/ats.v1.AtsService": "http://127.0.0.1:8095",
      "/healthz": "http://127.0.0.1:8095",
    },
  },
}));
