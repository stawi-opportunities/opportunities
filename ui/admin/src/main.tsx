import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { AppGate } from "@/components/AppGate";
import { Layout } from "@/components/Layout";
import { SourceList } from "@/pages/SourceList";
import { SourceTrace } from "@/pages/SourceTrace";
import { VariantTrace } from "@/pages/VariantTrace";
import { OpportunityTrace } from "@/pages/OpportunityTrace";
import { SeedDigest } from "@/pages/SeedDigest";
import { DefinitionsList } from "@/pages/DefinitionsList";
import { DefinitionEditor } from "@/pages/DefinitionEditor";
import { OpsOverview } from "@/pages/OpsOverview";
import { JobsList } from "@/pages/JobsList";
import { Rejections } from "@/pages/Rejections";
import "@/styles/admin.css";

const container = document.getElementById("admin-root");
if (!container) throw new Error("admin-root element missing");

createRoot(container).render(
  <StrictMode>
    <BrowserRouter basename="/admin">
      <AppGate>
        <Routes>
          <Route path="/" element={<Layout />}>
            <Route index element={<OpsOverview />} />
            <Route path="sources" element={<SourceList />} />
            <Route path="sources/:id" element={<SourceTrace />} />
            <Route path="jobs" element={<JobsList />} />
            <Route path="rejections" element={<Rejections />} />
            <Route path="variants/:id" element={<VariantTrace />} />
            <Route path="opportunities/:slug" element={<OpportunityTrace />} />
            <Route path="seeds/:id/digest" element={<SeedDigest />} />
            <Route path="definitions" element={<DefinitionsList />} />
            <Route
              path="definitions/:type/:name"
              element={<DefinitionEditor />}
            />
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </AppGate>
    </BrowserRouter>
  </StrictMode>,
);
