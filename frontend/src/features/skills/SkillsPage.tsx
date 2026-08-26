import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { fetchSkills, createSkill } from "@/features/skills/api";
import { GraphQLError } from "@/lib/graphql-client";
import { useAuthStore } from "@/lib/auth-store";
import { chipColor, cn, gradientButton } from "@/lib/utils";
import { ShieldAlert } from "lucide-react";

const schema = z.object({
  name: z.string().min(1, "Required"),
  category: z.string().min(1, "Required"),
});

type FormValues = z.infer<typeof schema>;

export function SkillsPage() {
  const queryClient = useQueryClient();
  const { data: skills, isLoading } = useQuery({ queryKey: ["skills"], queryFn: fetchSkills });
  const { accessToken, role } = useAuthStore();
  const isAdmin = role === "admin";

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { name: "", category: "" },
  });

  const mutation = useMutation({
    mutationFn: ({ name, category }: FormValues) => createSkill(accessToken!, name, category),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["skills"] });
      form.reset();
    },
  });

  return (
    <div className="mx-auto grid max-w-2xl gap-6 p-8">
      <Card className="animate-in-up">
        <CardHeader>
          <CardTitle className="bg-gradient-to-r from-fuchsia-600 via-violet-600 to-sky-600 bg-clip-text text-transparent">
            Skill taxonomy
          </CardTitle>
          <CardDescription>Every skill jobs can require and users can claim.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading && (
            <div className="flex flex-wrap gap-2">
              <div className="skeleton h-7 w-20 rounded-full" />
              <div className="skeleton h-7 w-24 rounded-full" />
              <div className="skeleton h-7 w-16 rounded-full" />
            </div>
          )}
          {skills && skills.length === 0 && (
            <p className="text-muted-foreground text-sm">No skills yet — add the first one below.</p>
          )}
          {skills && skills.length > 0 && (
            <div className="flex flex-wrap gap-2">
              {skills.map((s, i) => (
                <span
                  key={s.id}
                  className={cn(
                    "animate-in-up flex items-center gap-1.5 rounded-full px-3 py-1 text-sm font-medium",
                    chipColor(s.id),
                  )}
                  style={{ animationDelay: `${i * 25}ms` }}
                >
                  {s.name}
                  <span className="text-xs opacity-70">{s.category}</span>
                </span>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {isAdmin ? (
        <Card className="animate-in-up" style={{ animationDelay: "60ms" }}>
          <CardHeader>
            <CardTitle>Add a skill</CardTitle>
            <CardDescription>Admin only — skill-taxonomy management is RBAC-gated.</CardDescription>
          </CardHeader>
          <CardContent>
            <Form {...form}>
              <form
                className="grid gap-4"
                onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
              >
                <FormField
                  control={form.control}
                  name="name"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Name</FormLabel>
                      <FormControl>
                        <Input placeholder="e.g. TypeScript" {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="category"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Category</FormLabel>
                      <FormControl>
                        <Input placeholder="e.g. Languages" {...field} />
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
                  {mutation.isPending ? "Adding..." : "Add skill"}
                </Button>
              </form>
            </Form>
          </CardContent>
        </Card>
      ) : (
        <Card className="animate-in-up border-dashed" style={{ animationDelay: "60ms" }}>
          <CardContent className="text-muted-foreground flex items-center gap-2 py-6 text-sm">
            <ShieldAlert className="size-4 shrink-0" />
            {accessToken
              ? "Only admins can add new skills to the taxonomy."
              : "Log in as an admin to add new skills to the taxonomy."}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
