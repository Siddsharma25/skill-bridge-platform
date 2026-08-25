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

const schema = z.object({
  name: z.string().min(1, "Required"),
  category: z.string().min(1, "Required"),
});

type FormValues = z.infer<typeof schema>;

export function SkillsPage() {
  const queryClient = useQueryClient();
  const { data: skills, isLoading } = useQuery({ queryKey: ["skills"], queryFn: fetchSkills });

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { name: "", category: "" },
  });

  const mutation = useMutation({
    mutationFn: ({ name, category }: FormValues) => createSkill(name, category),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["skills"] });
      form.reset();
    },
  });

  return (
    <div className="mx-auto grid max-w-2xl gap-6 p-8">
      <Card>
        <CardHeader>
          <CardTitle>Skill taxonomy</CardTitle>
          <CardDescription>Every skill jobs can require and users can claim.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading && <p className="text-muted-foreground text-sm">Loading...</p>}
          {skills && skills.length === 0 && (
            <p className="text-muted-foreground text-sm">No skills yet — add the first one below.</p>
          )}
          {skills && skills.length > 0 && (
            <ul className="grid gap-2">
              {skills.map((s) => (
                <li key={s.id} className="flex items-center justify-between text-sm">
                  <span>{s.name}</span>
                  <span className="text-muted-foreground">{s.category}</span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Add a skill</CardTitle>
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
              <Button type="submit" disabled={mutation.isPending}>
                {mutation.isPending ? "Adding..." : "Add skill"}
              </Button>
            </form>
          </Form>
        </CardContent>
      </Card>
    </div>
  );
}
