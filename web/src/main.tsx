import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";
import { I18nProvider } from "./i18n";
import { SessionProvider } from "./session";
import { ToastProvider } from "./toast";
import { App } from "./App";
import { ErrorBoundary } from "./components/ErrorBoundary";
import "./styles.css";
import "./themes/modern/theme.css";
import "./themes/classic/theme.css";
import "./themes/classic/compat.css";
import "./styles/theme-gallery.css";
// Last on purpose: a palette ties on specificity with the theme token blocks,
// so cascade order is what lets it win. See styles/palettes.css.
import "./styles/palettes.css";

const queryClient = new QueryClient({
	defaultOptions: {
		queries: { staleTime: 15_000, retry: false, refetchOnWindowFocus: false },
		mutations: { retry: false },
	},
});

createRoot(document.getElementById("root")!).render(
	<StrictMode>
		<QueryClientProvider client={queryClient}>
			<I18nProvider>
				<ToastProvider>
					<SessionProvider>
						<BrowserRouter basename="/console">
							<ErrorBoundary>
								<App />
							</ErrorBoundary>
						</BrowserRouter>
					</SessionProvider>
				</ToastProvider>
			</I18nProvider>
		</QueryClientProvider>
	</StrictMode>,
);
