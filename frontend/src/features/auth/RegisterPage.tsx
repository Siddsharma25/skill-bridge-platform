import { useNavigate, Link } from "react-router-dom";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { register as registerRequest } from "@/features/auth/api";
import { useAuthStore } from "@/lib/auth-store";
import { GraphQLError } from "@/lib/graphql-client";
import { gradientButton } from "@/lib/utils";
import { decodeJwtRole } from "@/lib/jwt";
import { UserPlus } from "lucide-react";

const schema = z.object({
  email: z.string().email("Enter a valid email"),
  password: z.string().min(8, "Password must be at least 8 characters"),
});

type FormValues = z.infer<typeof schema>;

export function RegisterPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const setAuth = useAuthStore((s) => s.setAuth);

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { email: "", password: "" },
  });

  const mutation = useMutation({
    mutationFn: ({ email, password }: FormValues) => registerRequest(email, password),
    onSuccess: (data) => {
      setAuth(data.accessToken, data.userId, decodeJwtRole(data.accessToken));
      queryClient.clear();
      navigate("/profile", { replace: true });
    },
  });

  return (
    <div className="flex min-h-svh items-center justify-center p-8">
      <Card className="animate-in-up w-full max-w-sm shadow-lg">
        <CardHeader>
          <div className="mb-1 flex size-11 items-center justify-center rounded-full bg-gradient-to-br from-fuchsia-500 via-violet-500 to-sky-500 shadow-md shadow-violet-500/30">
            <UserPlus className="size-5 text-white" />
          </div>
          <CardTitle>Create an account</CardTitle>
          <CardDescription>Start matching your skills to job postings.</CardDescription>
        </CardHeader>
        <CardContent>
          <Form {...form}>
            <form
              className="grid gap-4"
              noValidate
              onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
            >
              <FormField
                control={form.control}
                name="email"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Email</FormLabel>
                    <FormControl>
                      <Input type="email" autoComplete="email" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="password"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Password</FormLabel>
                    <FormControl>
                      <Input type="password" autoComplete="new-password" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              {mutation.isError && (
                <p className="text-destructive text-sm">
                  {mutation.error instanceof GraphQLError
                    ? mutation.error.message
                    : "Something went wrong. Try again."}
                </p>
              )}
              <Button type="submit" disabled={mutation.isPending} className={gradientButton}>
                {mutation.isPending ? "Creating account..." : "Register"}
              </Button>
            </form>
          </Form>
          <p className="text-muted-foreground mt-4 text-sm">
            Already have an account?{" "}
            <Link to="/login" className="underline underline-offset-4">
              Log in
            </Link>
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
