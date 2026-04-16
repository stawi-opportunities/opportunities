/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./layouts/**/*.html",
    "./content/**/*.md",
    "./assets/js/**/*.js",
  ],
  theme: {
    extend: {
      colors: {
        navy: {
          50: "#f0f1f8",
          100: "#d9dbed",
          200: "#b3b7db",
          300: "#8d93c9",
          400: "#676fb7",
          500: "#414ba5",
          600: "#343c84",
          700: "#272d63",
          800: "#1a1e42",
          900: "#1a1a2e",
          950: "#0d0d17",
        },
        accent: {
          50: "#fef2f2",
          100: "#fee2e2",
          200: "#fecaca",
          300: "#fca5a5",
          400: "#f87171",
          500: "#e74c3c",
          600: "#dc2626",
          700: "#b91c1c",
          800: "#991b1b",
          900: "#7f1d1d",
        },
      },
      fontFamily: {
        sans: ["Inter", "system-ui", "-apple-system", "sans-serif"],
      },
    },
  },
  plugins: [
    require("@tailwindcss/forms"),
    require("@tailwindcss/typography"),
  ],
};
