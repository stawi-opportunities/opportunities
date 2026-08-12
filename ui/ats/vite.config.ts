import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5175,
    proxy: {
      // Connect procedures: /ats.v1.AtsService/*
      "/ats.v1.AtsService": "http://127.0.0.1:8095",
      "/healthz": "http://127.0.0.1:8095",
    },
  },
});
