/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./layouts/**/*.html",
    "./content/**/*.md",
    "./app/src/**/*.{ts,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        // Deep corporate navy — the primary brand colour. Used for text,
        // buttons, nav, and hero backgrounds.
        navy: {
          50: "#f2f4f8",
          100: "#e4e7ef",
          200: "#c4cadd",
          300: "#8e97b5",
          400: "#616e96",
          500: "#3f4b79",
          600: "#2c365d",
          700: "#1f2747",
          800: "#141a33",
          900: "#0c1226",
          950: "#050912",
        },
        // Stawi brand green — the leaf accent on the wordmark logo.
        // Palette hand-built around the brand values #45B739 / #219C3F.
        // Used for highlights, eyebrows, and "success" affordances; not
        // the primary button colour (that's navy).
        accent: {
          50: "#f0fdf4",
          100: "#dcfce7",
          200: "#bbf7d0",
          300: "#86efac",
          400: "#45b739",
          500: "#219c3f",
          600: "#198535",
          700: "#136b2a",
          800: "#0e5420",
          900: "#083c17",
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
