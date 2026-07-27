import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { ToastProvider } from "../toast";
import { useAdminMutation } from "./useAdminMutation";

function wrapperFor(client: QueryClient) {
	return function Wrapper({ children }: { children: ReactNode }) {
		return createElement(
			QueryClientProvider,
			{ client },
			createElement(
				I18nProvider,
				null,
				createElement(ToastProvider, null, children),
			),
		);
	};
}

describe("useAdminMutation", () => {
	it("invalidates configured query keys and tracks pendingId", async () => {
		const queryClient = new QueryClient({
			defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
		});
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		let resolveMutation: ((value: { id: number }) => void) | undefined;
		const mutationFn = vi.fn((id: number) => {
			void id;
			return new Promise<{ id: number }>((resolve) => {
				resolveMutation = resolve;
			});
		});

		const { result } = renderHook(
			() =>
				useAdminMutation({
					mutationFn: (id: number) => mutationFn(id),
					invalidateKeys: [["sites"], ["channels"]],
					pendingIdOf: (id) => id,
				}),
			{ wrapper: wrapperFor(queryClient) },
		);

		act(() => {
			result.current.mutate(42);
		});

		await waitFor(() => expect(result.current.isPending).toBe(true));
		expect(result.current.pendingId).toBe(42);

		await act(async () => {
			resolveMutation?.({ id: 42 });
		});

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(mutationFn).toHaveBeenCalledWith(42);
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["sites"] });
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["channels"] });
		expect(result.current.pendingId).toBeNull();
	});
});
