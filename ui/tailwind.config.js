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
      animation: {
        'fade-in': 'fadeIn 150ms ease-out',
        'slide-up': 'slideUp 200ms ease-out',
        'slide-down': 'slideDown 200ms ease-out',
        'confetti': 'confetti 1s ease-out forwards',
        'fade-up': 'fadeUp 500ms ease-out both',
        'float-y': 'floatY 3s ease-in-out infinite',
        'orb': 'orb 25s ease-in-out infinite',
        'orb-slow': 'orb 35s ease-in-out infinite reverse',
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' },
        },
        fadeUp: {
          '0%': { opacity: '0', transform: 'translateY(24px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        slideUp: {
          '0%': { transform: 'translateY(100%)' },
          '100%': { transform: 'translateY(0)' },
        },
        slideDown: {
          '0%': { opacity: '0', transform: 'translateY(-8px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        confetti: {
          '0%': { transform: 'translateY(0) rotate(0deg)', opacity: '1' },
          '100%': { transform: 'translateY(200px) rotate(720deg)', opacity: '0' },
        },
        floatY: {
          '0%, 100%': { transform: 'translateY(0)' },
          '50%': { transform: 'translateY(-12px)' },
        },
        orb: {
          '0%, 100%': { transform: 'translate(0, 0) scale(1)' },
          '25%': { transform: 'translate(30px, -40px) scale(1.08)' },
          '50%': { transform: 'translate(-20px, 20px) scale(0.95)' },
          '75%': { transform: 'translate(40px, 30px) scale(1.05)' },
        },
      },
    },
  },
  plugins: [
    require("@tailwindcss/forms"),
    require("@tailwindcss/typography"),
    function ({ matchUtilities, theme }) {
      matchUtilities(
        {
          'animation-delay': (value) => ({ animationDelay: value }),
        },
        { values: { ...theme('transitionDelay'), 400: '400ms' } }
      );
    },
  ],
};
