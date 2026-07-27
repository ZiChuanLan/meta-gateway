import {
	useMutation,
	useQueryClient,
	type QueryKey,
} from "@tanstack/react-query";
import { useToast } from "../toast";

type PendingId = string | number | null | undefined;

/**
 * Shared Admin mutation helper: invalidates query families on success,
 * exposes mutation error, tracks a pending row/entity id, and surfaces
 * failures as bottom-right toasts by default (not a page-top strip).
 */
export function useAdminMutation<TData, TVariables = void, TContext = unknown>(options: {
	mutationFn: (variables: TVariables) => Promise<TData>;
	/** Query keys to invalidate after a successful mutation. */
	invalidateKeys?: QueryKey[];
	/** Map variables → entity id for per-row pending UI. */
	pendingIdOf?: (variables: TVariables) => PendingId;
	/**
	 * When true (default), failed mutations show a bottom-right toast.
	 * Set false when the caller renders a local form/dialog error instead.
	 */
	toastOnError?: boolean;
	onSuccess?: (
		data: TData,
		variables: TVariables,
		context: TContext | undefined,
	) => void;
	onError?: (
		error: unknown,
		variables: TVariables,
		context: TContext | undefined,
	) => void;
	onSettled?: (
		data: TData | undefined,
		error: unknown,
		variables: TVariables,
		context: TContext | undefined,
	) => void;
}) {
	const queryClient = useQueryClient();
	const toast = useToast();
	const mutation = useMutation<TData, unknown, TVariables, TContext>({
		mutationFn: options.mutationFn,
		onSuccess: (data, variables, context) => {
			options.onSuccess?.(data, variables, context);
		},
		onError: (error, variables, context) => {
			if (options.toastOnError !== false) {
				toast.pushError(error);
			}
			options.onError?.(error, variables, context);
		},
		onSettled: async (data, error, variables, context) => {
			// Refresh lists after success *and* failure so health badges update
			// when probe/sync fails (e.g. bad base URL) without leaving stale "ready".
			if (options.invalidateKeys?.length) {
				await Promise.all(
					options.invalidateKeys.map((queryKey) =>
						queryClient.invalidateQueries({ queryKey }),
					),
				);
			}
			options.onSettled?.(data, error, variables, context);
		},
	});

	const pendingId: PendingId =
		mutation.isPending &&
		options.pendingIdOf &&
		mutation.variables !== undefined
			? options.pendingIdOf(mutation.variables)
			: null;

	return {
		...mutation,
		/** Entity id currently mid-flight, when pendingIdOf is provided. */
		pendingId,
	};
}
